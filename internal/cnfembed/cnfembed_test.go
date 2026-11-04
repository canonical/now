package cnfembed_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/canonical/now/internal/cnfembed"
)

// This file is a trival smoke test over cnfembed that may be
// copied with cnfembed.go. The full conformance suite is
// run on the original repository:
//
// https://github.com/canonical/go-cnf

func TestSmokeExample(t *testing.T) {
	j, err := cnfembed.ToJSON([]byte(cnfStr), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "json", string(j), trimJSON(jsonStr))
	var v any
	if err := json.Unmarshal(j, &v); err != nil {
		t.Fatalf("cannot decode output: %v", err)
	}
}

func TestSmokeErrors(t *testing.T) {
	for _, tt := range cnfErrors {
		t.Run(tt.error, func(t *testing.T) {
			_, err := cnfembed.ToJSON([]byte(tt.cnf), nil)
			if err == nil || err.Error() != tt.error {
				t.Fatalf("error = %v, want %q", err, tt.error)
			}
		})
	}
}

func TestEscape(t *testing.T) {
	assertEqual(t, "Escape", cnfembed.Escape("a\"b\nc"), "a\\\"b\\nc")
}

func assertEqual(t *testing.T, label string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", label, got, want)
	}
}

func trimJSON(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "")
}

// The smoke fixture is the spec's own Example, holding most constructs.
var cnfStr = `
key: "value"
nested.key: "another value"

map:
    inline: {a: 1, b: 2}

    block:
        a: 1
        b: 2

    block.c: 3

list:
    inline: [1, 2, 3]

    block.strings:
        - "item 1"
        - "item 2"

    block.maps:
        - label: "map 1"
        - label: "map 2"

    block.list-of-maps:
        ---
        list1.map1:
            a: 1
            b: 2
        list1.map2:
            c: 3
            d: 4
        ---
        list2.map1:
            a: 1
            b: 2
        ---

    block.list-of-lists:
        ---
        - "list 1 item 1"
        - "list 1 item 2"
        ---
        - "list 2 item 1"
        ---

string:
    double: "line 1\nline 2\nline 3"

    # One line, no escaping, literal slash
    single: 'line 1\nline 1 still'

    # Three lines, with escaping support.
    fence.double.inline: """line 1\nLine 2\nline 3\n"""
    fence.double.block:
        """
        line 1\nline \
        2
        line 3
        """

    # One line, no escaping, literal slash
    fence.single.inline: '''line 1\nline 1 still'''
    fence.single.block:
        '''
        line 1\n
        line 2
        '''

numbers:
    int: 123
    float: 1.23
    hex: 0x123
    octal: 0o777
    binary: 0b011
    exponent: 1e-23
`

var jsonStr = `
{
    "key":"value",
    "nested":{"key":"another value"},
    "map":{
        "inline":{"a":1,"b":2},
        "block":{"a":1,"b":2,"c":3}
    },
    "list":{
        "inline":[1,2,3],
        "block":{
            "strings":["item 1","item 2"],
            "maps":[{"label":"map 1"},{"label":"map 2"}],
            "list-of-maps":[
                {"list1":{"map1":{"a":1,"b":2},"map2":{"c":3,"d":4}}},
                {"list2":{"map1":{"a":1,"b":2}}}
            ],
            "list-of-lists":[
                ["list 1 item 1","list 1 item 2"],
                ["list 2 item 1"]
            ]
        }
    },
    "string":{
        "double":"line 1\nline 2\nline 3",
        "single":"line 1\\nline 1 still",
        "fence":{
            "double":{
                "inline":"line 1\nLine 2\nline 3\n",
                "block":"line 1\nline 2\nline 3\n"
            },
            "single":{
                "inline":"line 1\\nline 1 still",
                "block":"line 1\\n\nline 2\n"
            }
        }
    },
    "numbers":{
        "int":123,
        "float":1.23,
        "hex":291,
        "octal":511,
        "binary":3,
        "exponent":1e-23
    }
}
`

var cnfErrors = []struct{ cnf, error string }{{
	cnf:   `a: "x`,
	error: "1:4: cannot find closing quote",
}, {
	cnf:   "a: {b: 1\n",
	error: "1:4: cannot find closing `}`",
}, {
	cnf:   "a: 1\n\tb: 2\n",
	error: "2:2: cannot mix indentation prefixes",
}, {
	cnf:   "a:\n b:\n\t\tc: 1\n",
	error: "3:3: cannot mix indentation prefixes",
}, {
	cnf:   "a:\n    \"\"\"\ncontent\n",
	error: "3:1: cannot close fence at shallower prefix",
}, {
	cnf:   "1\nbogus\n",
	error: "2:1: cannot parse content after top-level value: `bogus`",
}, {
	cnf:   "a 1\n",
	error: "1:1: cannot parse unquoted value `a`",
}, {
	cnf:   "a: bare\n",
	error: "1:4: cannot parse unquoted value `bare`",
}, {
	cnf:   "a..b: 1\n",
	error: "1:3: cannot parse empty key segment",
}, {
	cnf:   "a .b: 1\n",
	error: "1:2: cannot parse space in path",
}, {
	cnf:   "a:\n",
	error: `1:1: cannot omit the value for "a"`,
}, {
	cnf:   "a:\n    b.c:\n",
	error: "2:5: cannot omit the value for `b.c`",
}, {
	cnf:   "a: 1\na: 2\n",
	error: `2:1: cannot redefine previous key "a"`,
}, {
	cnf:   "a: 1\na.b: 2\n",
	error: `2:1: cannot redefine previous key "a"`,
}, {
	cnf:   "a: {b: 1}\na: {c: 2}\n",
	error: `2:1: cannot expand map key "a" more than once`,
}, {
	cnf:   "a: \"x\\dy\"\n",
	error: "1:6: cannot parse escape \\d",
}, {
	cnf:   "a: 02\n",
	error: "1:4: cannot parse number `02`: redundant leading zero",
}, {
	cnf:   "a: 1E5\n",
	error: "1:4: cannot parse number `1E5`: uppercase exponent",
}}
