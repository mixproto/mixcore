// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A JSON Schema (2020-12) checker for exactly the keywords the core-api
// contract uses, so the contract tests need no third-party module
// (review Go S4): type, enum, const, required, properties,
// additionalProperties false, items, minItems, maxItems, uniqueItems,
// minimum, maximum, minLength, maxLength, $ref (local and to another file
// of contracts/core-api), allOf, anyOf, oneOf, not, if/then/else.
// Annotations ($schema, $id, title, description, default, examples) are
// ignored. TestSchemaCheck_RejectsBadInput keeps it honest.

type schemaSet struct{ docs map[string]any }

func loadSchemas(t testing.TB) *schemaSet {
	t.Helper()
	files, err := filepath.Glob("../contracts/core-api/*.schema.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("schemas: %v", err)
	}
	s := &schemaSet{docs: map[string]any{}}
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // G304: a fixed contract directory
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		s.docs[filepath.Base(f)] = doc
	}
	return s
}

// validate checks a JSON text against one schema file; it returns the
// violations ("path: problem").
func (s *schemaSet) validate(file, instance string) []string {
	var inst any
	if err := json.Unmarshal([]byte(instance), &inst); err != nil {
		return []string{"not JSON: " + err.Error()}
	}
	var errs []string
	s.check(file, s.docs[file], inst, "$", &errs)
	return errs
}

func (s *schemaSet) check(file string, schema, inst any, path string, errs *[]string) {
	node, ok := schema.(map[string]any)
	if !ok {
		return
	}
	if ref, ok := node["$ref"].(string); ok {
		f, target := s.resolve(file, ref)
		s.check(f, target, inst, path, errs)
	}
	s.checkValue(node, inst, path, errs)
	s.checkObject(file, node, inst, path, errs)
	s.checkArray(file, node, inst, path, errs)
	s.checkCombinators(file, node, inst, path, errs)
}

// resolve follows a $ref: "#/$defs/x" in file, or "other.schema.json#/..."
func (s *schemaSet) resolve(file, ref string) (string, any) {
	doc, pointer, _ := strings.Cut(ref, "#")
	if doc != "" {
		file = doc
	}
	node := s.docs[file]
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		if part == "" {
			continue
		}
		m, _ := node.(map[string]any)
		node = m[part]
	}
	return file, node
}

func (s *schemaSet) valid(file string, schema, inst any) bool {
	var errs []string
	s.check(file, schema, inst, "", &errs)
	return len(errs) == 0
}

func fail(errs *[]string, path, format string, args ...any) {
	*errs = append(*errs, path+": "+fmt.Sprintf(format, args...))
}

func typeOK(want string, inst any) bool {
	switch want {
	case "object":
		_, ok := inst.(map[string]any)
		return ok
	case "array":
		_, ok := inst.([]any)
		return ok
	case "string":
		_, ok := inst.(string)
		return ok
	case "boolean":
		_, ok := inst.(bool)
		return ok
	case "null":
		return inst == nil
	case "number":
		_, ok := inst.(float64)
		return ok
	case "integer":
		f, ok := inst.(float64)
		return ok && f == math.Trunc(f)
	}
	return false
}

func (s *schemaSet) checkValue(node map[string]any, inst any, path string, errs *[]string) {
	switch t := node["type"].(type) {
	case string:
		if !typeOK(t, inst) {
			fail(errs, path, "type %s, got %v", t, inst)
		}
	case []any:
		ok := false
		for _, w := range t {
			ok = ok || typeOK(w.(string), inst)
		}
		if !ok {
			fail(errs, path, "type %v, got %v", t, inst)
		}
	}
	if c, has := node["const"]; has && !reflect.DeepEqual(c, inst) {
		fail(errs, path, "const %v, got %v", c, inst)
	}
	if e, has := node["enum"].([]any); has {
		ok := false
		for _, v := range e {
			ok = ok || reflect.DeepEqual(v, inst)
		}
		if !ok {
			fail(errs, path, "enum %v, got %v", e, inst)
		}
	}
	s.checkBounds(node, inst, path, errs)
}

func (s *schemaSet) checkBounds(node map[string]any, inst any, path string, errs *[]string) {
	if f, ok := inst.(float64); ok {
		if m, has := node["minimum"].(float64); has && f < m {
			fail(errs, path, "%v below minimum %v", f, m)
		}
		if m, has := node["maximum"].(float64); has && f > m {
			fail(errs, path, "%v above maximum %v", f, m)
		}
	}
	if str, ok := inst.(string); ok {
		if m, has := node["minLength"].(float64); has && float64(len([]rune(str))) < m {
			fail(errs, path, "shorter than %v", m)
		}
		if m, has := node["maxLength"].(float64); has && float64(len([]rune(str))) > m {
			fail(errs, path, "longer than %v", m)
		}
	}
}

func (s *schemaSet) checkObject(file string, node map[string]any, inst any, path string, errs *[]string) {
	obj, ok := inst.(map[string]any)
	if !ok {
		return
	}
	props, _ := node["properties"].(map[string]any)
	for _, r := range asSlice(node["required"]) {
		if _, has := obj[r.(string)]; !has {
			fail(errs, path, "%q is required", r)
		}
	}
	for k, v := range obj {
		if p, has := props[k]; has {
			s.check(file, p, v, path+"."+k, errs)
		} else if ap, set := node["additionalProperties"].(bool); set && !ap {
			fail(errs, path, "unknown property %q", k)
		}
	}
}

func (s *schemaSet) checkArray(file string, node map[string]any, inst any, path string, errs *[]string) {
	arr, ok := inst.([]any)
	if !ok {
		return
	}
	if m, has := node["minItems"].(float64); has && float64(len(arr)) < m {
		fail(errs, path, "fewer than %v items", m)
	}
	if m, has := node["maxItems"].(float64); has && float64(len(arr)) > m {
		fail(errs, path, "more than %v items", m)
	}
	for i, v := range arr {
		if items, has := node["items"]; has {
			s.check(file, items, v, fmt.Sprintf("%s[%d]", path, i), errs)
		}
		if u, _ := node["uniqueItems"].(bool); u {
			for _, w := range arr[:i] {
				if reflect.DeepEqual(v, w) {
					fail(errs, path, "items not unique")
				}
			}
		}
	}
}

func (s *schemaSet) checkCombinators(file string, node map[string]any, inst any, path string, errs *[]string) {
	for _, sub := range asSlice(node["allOf"]) {
		s.check(file, sub, inst, path, errs)
	}
	if subs := asSlice(node["anyOf"]); len(subs) > 0 && s.matches(file, subs, inst) == 0 {
		fail(errs, path, "matches no anyOf branch")
	}
	if subs := asSlice(node["oneOf"]); len(subs) > 0 {
		if n := s.matches(file, subs, inst); n != 1 {
			fail(errs, path, "matches %d oneOf branches, want 1", n)
		}
	}
	if not, has := node["not"]; has && s.valid(file, not, inst) {
		fail(errs, path, "matches a not schema")
	}
	if cond, has := node["if"]; has {
		branch := node["else"]
		if s.valid(file, cond, inst) {
			branch = node["then"]
		}
		if branch != nil {
			s.check(file, branch, inst, path, errs)
		}
	}
}

func (s *schemaSet) matches(file string, subs []any, inst any) int {
	n := 0
	for _, sub := range subs {
		if s.valid(file, sub, inst) {
			n++
		}
	}
	return n
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// The checker must find what the contract forbids, or every check built
// on it would pass vacuously.
func TestSchemaCheck_RejectsBadInput(t *testing.T) {
	s := loadSchemas(t)
	for name, tc := range map[string]struct{ file, json string }{
		"wrong type":       {"state.schema.json", `{"version":0,"type":"diff","seq":"1","status":"live","changes":[]}`},
		"missing required": {"state.schema.json", `{"version":0,"type":"snapshot","seq":1,"status":"live"}`},
		"unknown property": {"state.schema.json", `{"version":0,"type":"diff","seq":1,"status":"live","changes":[],"x":1}`},
		"bad enum":         {"state.schema.json", `{"version":0,"type":"diff","seq":1,"status":"asleep","changes":[]}`},
		"send without bus": {"command.schema.json", `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"sendLevel"},"value":0,"origin":{"kind":"gesture"}}`},
		"bus on a fader":   {"command.schema.json", `{"version":0,"type":"set","param":{"strip":{"kind":"input","index":1},"field":"fader","bus":2},"value":0,"origin":{"kind":"gesture"}}`},
		"pan out of range": {"state.schema.json", `{"version":0,"type":"snapshot","seq":1,"status":"live","strips":[{"kind":"input","index":1,"name":"","faderDB":0,"mute":false,"sends":[{"bus":1,"levelDB":0,"pan":120}]}]}`},
		"bad bus kind":     {"state.schema.json", `{"version":0,"type":"snapshot","seq":1,"status":"live","strips":[],"buses":[{"index":1,"kind":"aux","name":"","faderDB":0,"mute":false}]}`},
		"monitor no buses": {"config.schema.json", `{"version":0,"family":"x32","host":"10.0.0.1","role":{"kind":"monitor"}}`},
		"two oneOf events": {"state.schema.json", `{"version":0,"type":"diff","seq":1,"status":"live","changes":[],"events":[{"type":"gainUnlockEnded"}]}`},
	} {
		if errs := s.validate(tc.file, tc.json); len(errs) == 0 {
			t.Fatalf("%s: accepted %s", name, tc.json)
		}
	}
	if errs := s.validate("command.schema.json", sendCmd("set", "sendPan", 4, 1, "-40", "ui")); len(errs) != 0 {
		t.Fatalf("a valid command refused: %v", errs)
	}
}

// Every example printed in the command and config schemas is valid
// against its own schema (and, by TestContractExamples_Parse, accepted).
func TestSchemaCheck_ContractExamplesAreValid(t *testing.T) {
	s := loadSchemas(t)
	for _, file := range []string{"command.schema.json", "config.schema.json", "discover.schema.json", "demo.schema.json", "gesture-result.schema.json"} {
		doc, _ := s.docs[file].(map[string]any)
		for i, ex := range asSlice(doc["examples"]) {
			b, _ := json.Marshal(ex)
			if errs := s.validate(file, string(b)); len(errs) != 0 {
				t.Fatalf("%s example %d: %v", file, i, errs)
			}
		}
	}
}
