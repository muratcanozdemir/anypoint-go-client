package yamlx

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestPolicyDefinition(t *testing.T) {
	src := `---
# a policy definition
id: rate-limiting
name: Rate limiting
description: |
  Limits requests.
  Second line.
category: Quality of service
type: system
resourceLevelSupported: true
standalone: true
requiredCharacteristics: []
providedCharacteristics: [Requires authentication, "Rate: limited"]
configuration:
  - propertyName: rateLimits
    name: Limits
    type: keyvalues
    optional: false
    allowMultiple: true
    configuration:
      - propertyName: maximumRequests
        type: int
        defaultValue: 10
  - propertyName: exposeHeaders
    type: radio
    options:
    - name: Yes
      value: true
    - {name: No, value: false}
    description: >
      Folded
      text here.
  - propertyName: "quoted: key"
    defaultValue: 'it''s'   # trailing comment
    pattern: "#[attributes.headers['x']]"
`
	got, err := Map([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"category":"Quality of service","configuration":[{"allowMultiple":true,"configuration":[{"defaultValue":10,"propertyName":"maximumRequests","type":"int"}],"name":"Limits","optional":false,"propertyName":"rateLimits","type":"keyvalues"},{"description":"Folded text here.\n","options":[{"name":"Yes","value":true},{"name":"No","value":false}],"propertyName":"exposeHeaders","type":"radio"},{"defaultValue":"it's","pattern":"#[attributes.headers['x']]","propertyName":"quoted: key"}],"description":"Limits requests.\nSecond line.\n","id":"rate-limiting","name":"Rate limiting","providedCharacteristics":["Requires authentication","Rate: limited"],"requiredCharacteristics":[],"resourceLevelSupported":true,"standalone":true,"type":"system"}`
	b, _ := json.Marshal(got)
	if string(b) != want {
		t.Errorf("got\n%s\nwant\n%s", b, want)
	}
}

func TestEdgeCases(t *testing.T) {
	for src, want := range map[string]string{
		"":                          `null`,
		"- a\n- - b\n  - c\n-\n":    `["a",["b","c"],null]`,
		"k:\n- x\n- y\nz: 1":        `{"k":["x","y"],"z":1}`,
		"a: |-\n  x\n\n  y\n\nb: 2": `{"a":"x\n\ny","b":2}`,
		"url: http://x/y#z":         `{"url":"http://x/y#z"}`,
		"v: 1.10\nw: 0x1F":          `{"v":1.1,"w":"0x1F"}`,
	} {
		v, err := Unmarshal([]byte(src))
		if err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if b, _ := json.Marshal(v); string(b) != want {
			t.Errorf("%q: got %s, want %s", src, b, want)
		}
	}
	for _, bad := range []string{"a: 1\n  b: 2", "a: 1\na: 2", "a:\n\t- x", "just text\nmore: x"} {
		if _, err := Unmarshal([]byte(bad)); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
	if _, err := Map([]byte("- a")); !errors.Is(err, ErrNotMapping) {
		t.Errorf("Map of a sequence: %v", err)
	}
}
