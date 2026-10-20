// SPDX-License-Identifier: Apache-2.0

package api

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mayura-andrew/eventpass/internal/mail"
)

// The OpenAPI file is the source of truth. These tests fail when the route
// table or the response types drift from it.

type spec struct {
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Schemas map[string]struct {
			Properties map[string]yaml.Node `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func loadSpec(t *testing.T) spec {
	t.Helper()
	b, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var s spec
	if err := yaml.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

var httpMethods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}

func TestOpenAPIMatchesRoutes(t *testing.T) {
	s := loadSpec(t)
	inSpec := map[string]string{} // "METHOD /path" → x-permission
	for path, ops := range s.Paths {
		for method, node := range ops {
			if !httpMethods[method] {
				continue
			}
			var op struct {
				Perm string `yaml:"x-permission"`
				ID   string `yaml:"operationId"`
			}
			if err := node.Decode(&op); err != nil {
				t.Fatal(err)
			}
			key := strings.ToUpper(method) + " " + path
			if op.Perm == "" || op.ID == "" {
				t.Errorf("%s: every operation needs operationId and x-permission", key)
			}
			inSpec[key] = op.Perm
		}
	}
	routes := map[string]string{}
	for _, rt := range New(Options{}).routes() {
		routes[rt.Method+" "+rt.Path] = string(rt.Perm)
	}
	for key, perm := range routes {
		specPerm, ok := inSpec[key]
		switch {
		case !ok:
			t.Errorf("route %s is not in api/openapi.yaml", key)
		case specPerm != perm:
			t.Errorf("%s: spec says x-permission %q, server checks %q", key, specPerm, perm)
		}
	}
	for key := range inSpec {
		if _, ok := routes[key]; !ok {
			t.Errorf("api/openapi.yaml has %s but the server has no such route", key)
		}
	}
}

// schemaTypes maps component schemas to the Go types that encode them.
var schemaTypes = map[string]any{
	"User":           userJSON{},
	"Event":          eventJSON{},
	"Field":          fieldJSON{},
	"Checkpoint":     checkpointJSON{},
	"Participant":    participantJSON{},
	"AuditEntry":     auditJSON{},
	"Activity":       activityJSON{},
	"CheckpointStat": checkpointStat{},
	"EventRoleGrant": roleJSON{},
	"Registration":   myRegistrationJSON{},
	"CaughtMail":     mail.Caught{},
}

func TestOpenAPIMatchesTypes(t *testing.T) {
	s := loadSpec(t)
	for name, v := range schemaTypes {
		schema, ok := s.Components.Schemas[name]
		if !ok {
			t.Errorf("schema %s missing from api/openapi.yaml", name)
			continue
		}
		var specProps []string
		for p := range schema.Properties {
			specProps = append(specProps, p)
		}
		goProps := jsonNames(reflect.TypeOf(v))
		sort.Strings(specProps)
		sort.Strings(goProps)
		if !reflect.DeepEqual(specProps, goProps) {
			t.Errorf("schema %s drifted:\n spec: %v\n   go: %v", name, specProps, goProps)
		}
	}
}

// jsonNames lists the JSON property names of a struct, following embedded
// structs the way encoding/json does.
func jsonNames(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if f.Anonymous && tag == "" {
			out = append(out, jsonNames(f.Type)...)
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, name)
	}
	return out
}
