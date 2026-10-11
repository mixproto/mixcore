/* This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at https://mozilla.org/MPL/2.0/. */

/* Smoke test of the c-shared core API (make capi-check): start the engine
 * against a running mixsim, apply one fader gesture, read meter frames, and
 * time Apply and MeterFrame per call (spike S3). Usage: smoke <mixsim port>. */
#define _POSIX_C_SOURCE 200809L
#if defined(__APPLE__)
#define _DARWIN_C_SOURCE /* CLOCK_MONOTONIC_RAW: 42 ns resolution instead of 1 us */
#endif
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "libmixcore.h"

enum { SAMPLES = 10000, WAIT_STEPS = 500, MSG_CAP = 65536 };
static const double BUDGET_P95_US = 500.0; /* S3 target: p95 < 0.5 ms */

static pthread_mutex_t mu = PTHREAD_MUTEX_INITIALIZER;
static int live_seen, echo_seen, log_lines;

static int contains(const uint8_t* data, int32_t len, const char* needle) {
	static char buf[MSG_CAP];
	size_t n = (size_t)len < sizeof buf - 1 ? (size_t)len : sizeof buf - 1;
	memcpy(buf, data, n); /* the pointer is valid only during the callback */
	buf[n] = 0;
	return strstr(buf, needle) != NULL;
}

static void on_state(const uint8_t* data, int32_t len, intptr_t ctx) {
	(void)ctx;
	pthread_mutex_lock(&mu);
	if (contains(data, len, "\"status\":\"live\"")) live_seen = 1;
	if (contains(data, len, "\"type\":\"diff\"") && contains(data, len, "\"clientId\":\"smoke\"")) echo_seen = 1;
	pthread_mutex_unlock(&mu);
}

static void on_log(int32_t level, const uint8_t* line, int32_t len, intptr_t ctx) {
	(void)level; (void)line; (void)len; (void)ctx;
	pthread_mutex_lock(&mu);
	log_lines++;
	pthread_mutex_unlock(&mu);
}

static int flag(int* f) {
	pthread_mutex_lock(&mu);
	int v = *f;
	pthread_mutex_unlock(&mu);
	return v;
}

static void sleep_ms(long ms) {
	struct timespec ts = {ms / 1000, (ms % 1000) * 1000000L};
	nanosleep(&ts, NULL);
}

static int wait_for(int* f) {
	for (int i = 0; i < WAIT_STEPS && !flag(f); i++) sleep_ms(10);
	return flag(f);
}

#ifdef CLOCK_MONOTONIC_RAW
#define SMOKE_CLOCK CLOCK_MONOTONIC_RAW
#else
#define SMOKE_CLOCK CLOCK_MONOTONIC
#endif

static double now_us(void) {
	struct timespec ts;
	clock_gettime(SMOKE_CLOCK, &ts);
	return (double)ts.tv_sec * 1e6 + (double)ts.tv_nsec / 1e3;
}

static int cmp_double(const void* a, const void* b) {
	double x = *(const double*)a, y = *(const double*)b;
	return (x > y) - (x < y);
}

/* report prints p50/p95/p99/max and returns p95. */
static double report(const char* name, double* us, int n) {
	qsort(us, (size_t)n, sizeof us[0], cmp_double);
	double p95 = us[(int)(0.95 * n)];
	printf("%s, n=%d: p50=%.3fus p95=%.3fus p99=%.3fus max=%.3fus\n", name, n, us[n / 2], p95, us[(int)(0.99 * n)], us[n - 1]);
	return p95;
}

#define CHECK(cond, ...) do { if (!(cond)) { fprintf(stderr, "smoke: FAIL: " __VA_ARGS__); fputc('\n', stderr); exit(1); } } while (0)

static void gesture(void) {
	static const char* steps[] = {
		"{\"version\":0,\"type\":\"holdBegin\",\"param\":{\"strip\":{\"kind\":\"input\",\"index\":1},\"field\":\"fader\"},\"origin\":{\"kind\":\"gesture\",\"clientId\":\"smoke\",\"seq\":1}}",
		"{\"version\":0,\"type\":\"set\",\"param\":{\"strip\":{\"kind\":\"input\",\"index\":1},\"field\":\"fader\"},\"value\":-10,\"origin\":{\"kind\":\"gesture\",\"clientId\":\"smoke\",\"seq\":2}}",
		"{\"version\":0,\"type\":\"holdEnd\",\"param\":{\"strip\":{\"kind\":\"input\",\"index\":1},\"field\":\"fader\"},\"origin\":{\"kind\":\"gesture\",\"clientId\":\"smoke\",\"seq\":3}}",
	};
	for (size_t i = 0; i < sizeof steps / sizeof steps[0]; i++) {
		int32_t rc = MixApply((char*)steps[i]);
		CHECK(rc == MIX_OK, "gesture step %zu: code %d", i, rc);
	}
	CHECK(wait_for(&echo_seen), "the echo of the fader write never arrived");
	printf("gesture: holdBegin, set ch1 -10 dB, holdEnd; echo with our origin received\n");
}

static void meters(uint8_t* frame) {
	int32_t n = 0;
	for (int i = 0; i < WAIT_STEPS && (n = MixMeterFrame(frame, MIX_METER_FRAME_MAX_BYTES)) <= 24; i++) sleep_ms(10);
	CHECK(n == 24 + 32 * 4 && memcmp(frame, "MXMF", 4) == 0 && frame[6] == 32, "meter frame of %d bytes", n);
	uint8_t small[8] = {0};
	CHECK(MixMeterFrame(small, (int32_t)sizeof small) == n && small[0] == 0, "short buffer must not be written");
	CHECK(MixMeterFrame(NULL, 0) == n, "size query");
	printf("meters: frame of %d bytes, 32 input levels\n", n);
}

static int timing(uint8_t* frame) {
	static double us[SAMPLES];
	char cmd[512];
	for (int i = 0; i < SAMPLES; i++) {
		snprintf(cmd, sizeof cmd, "{\"version\":0,\"type\":\"set\",\"param\":{\"strip\":{\"kind\":\"input\",\"index\":2},"
			"\"field\":\"fader\"},\"value\":%d,\"origin\":{\"kind\":\"gesture\",\"clientId\":\"smoke\",\"seq\":%d}}", -(i % 40), i + 10);
		double t0 = now_us();
		int32_t rc = MixApply(cmd);
		us[i] = now_us() - t0;
		CHECK(rc == MIX_OK, "MixApply %d: code %d", i, rc);
	}
	double apply = report("MixApply", us, SAMPLES);
	for (int i = 0; i < SAMPLES; i++) {
		double t0 = now_us();
		int32_t n = MixMeterFrame(frame, MIX_METER_FRAME_MAX_BYTES);
		us[i] = now_us() - t0;
		CHECK(n > 0, "MixMeterFrame %d: %d", i, n);
	}
	double meter = report("MixMeterFrame", us, SAMPLES);
	return apply <= BUDGET_P95_US && meter <= BUDGET_P95_US;
}

int main(int argc, char** argv) {
	CHECK(argc == 2, "usage: smoke <mixsim port>");
	CHECK(MixSetLogSink(on_log, 0, MIX_LOG_INFO) == MIX_OK, "log sink");
	CHECK(MixSetStateListener(on_state, 0) == MIX_OK, "state listener");
	CHECK(MixApply("{") == MIX_ERR_INVALID_COMMAND, "bad JSON must give MIX_ERR_INVALID_COMMAND");
	CHECK(MixStop() == MIX_ERR_NOT_STARTED, "stop before start");
#ifdef MIX_CAPITEST
	CHECK(MixTestPanic() == MIX_ERR_INTERNAL, "a Go panic must come back as MIX_ERR_INTERNAL");
	printf("panic: recovered as MIX_ERR_INTERNAL\n");
#endif
	char config[256];
	snprintf(config, sizeof config, "{\"version\":0,\"family\":\"x32\",\"host\":\"127.0.0.1\",\"port\":%s,\"meters\":{\"inputs\":true}}", argv[1]);
	char* session = NULL;
	int32_t len = 0;
	CHECK(MixStart(config, &session, &len) == MIX_OK && session != NULL && len > 0, "MixStart");
	printf("session: %.*s\n", (int)len, session);
	MixFree(session);
	CHECK(wait_for(&live_seen), "engine never went live");
	printf("state: live\n");
	CHECK(MixFaderPosition(0.0) == 0.75, "fader law");

	static uint8_t frame[MIX_METER_FRAME_MAX_BYTES];
	gesture();
	meters(frame);
	int ok = timing(frame);
	CHECK(MixStop() == MIX_OK, "MixStop");
	MixSetStateListener(NULL, 0);
	MixSetLogSink(NULL, 0, MIX_LOG_INFO);
	printf("log lines received: %d\n", flag(&log_lines));
	CHECK(ok, "p95 above %.0f us", BUDGET_P95_US);
	printf("smoke: PASS\n");
	return 0;
}
