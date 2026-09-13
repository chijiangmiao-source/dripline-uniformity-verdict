package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJSONScannerMatchesEncodingJson feeds a corpus of documents — none of
// them containing leading-zero numbers — through both parseJSONObject and
// encoding/json, and pins that the two agree on acceptance and produce the
// same members. The only documents where the scanner may diverge are the
// leading-zero forms covered by TestJSONScannerToleratesLeadingZeros.
func TestJSONScannerMatchesEncodingJson(t *testing.T) {
	docs := []string{
		// Valid documents.
		`{}`,
		` { } `,
		`{"a":1}`,
		`{"a":1,"b":[1,2,{"c":"x"}],"d":null}`,
		`{"a": true, "b": false, "c": null}`,
		`{"a":"str with \" escapes \\ \/ \b\f\n\r\t and A"}`,
		`{"a":""}`,
		`{"a":0,"b":-0,"c":0.5,"d":10,"e":1e3,"f":1E+3,"g":1.5e-3,"h":100.001}`,
		`{"a":[[]],"b":{}}`,
		`{"a":1,"a":2}`,
		`{"䁥bc":[]}`,
		`{"a":-0.5}`,
		// Null decodes to a nil map without error, like encoding/json.
		`null`,
		// Invalid documents.
		``,
		` `,
		`{`,
		`}`,
		`[1,2,3]`,
		`"str"`,
		`5`,
		`true`,
		`tru`,
		`nul`,
		`nullx`,
		`{,}`,
		`{"a"}`,
		`{"a":}`,
		`{"a":1,}`,
		`{"a":1 "b":2}`,
		`{"a":1,,"b":2}`,
		`{'a':1}`,
		`{a:1}`,
		`{"a":1, "b":2`,
		`{"a":1}}`,
		`{"a":1} {"b":2}`,
		`{"a":1}x`,
		`{"a":"unterminated}`,
		`{"a":"bad \x escape"}`,
		`{"a":"\u12"}`,
		`{"a":"\u12zz"}`,
		`{"a":"tab	char"}`,
		`{"a":1.}`,
		`{"a":.5}`,
		`{"a":+1}`,
		`{"a":1e}`,
		`{"a":1e+}`,
		`{"a":--1}`,
		`{"a":0x1}`,
		`{"a":1..2}`,
		`{"a":NaN}`,
		`{"a":Infinity}`,
		`{"a":[1,]}`,
		`{"a":[1 2]}`,
		`{"a":[1,2}`,
		`{"a":truex}`,
		`{"a":nullx}`,
		`{"a":1x}`,
		`{"a":1 2}`,
	}
	for _, doc := range docs {
		t.Run(doc, func(t *testing.T) {
			var std map[string]json.RawMessage
			stdErr := json.Unmarshal([]byte(doc), &std)

			got, err := parseJSONObject([]byte(doc))
			if stdErr != nil {
				require.Error(t, err, "encoding/json rejects %q, scanner must too", doc)
				return
			}
			require.NoError(t, err, "encoding/json accepts %q, scanner must too", doc)
			if std == nil {
				assert.Nil(t, got)
				return
			}
			require.Equal(t, len(std), len(got), "member count for %q", doc)
			for k, v := range std {
				assert.JSONEq(t, string(v), string(got[k]), "member %q of %q", k, doc)
			}
		})
	}
}

// TestJSONScannerToleratesLeadingZeros pins the single extension over strict
// JSON: number tokens with leading zeros parse, and their raw text is kept so
// field validation can reject them with a located error.
func TestJSONScannerToleratesLeadingZeros(t *testing.T) {
	docs := []struct {
		doc  string
		want map[string]string
	}{
		{`{"a":01}`, map[string]string{"a": "01"}},
		{`{"a":00}`, map[string]string{"a": "00"}},
		{`{"a":007.5}`, map[string]string{"a": "007.5"}},
		{`{"a":-01}`, map[string]string{"a": "-01"}},
		{`{"a":00.5e2}`, map[string]string{"a": "00.5e2"}},
		{`{"a":[01, 2, 03]}`, map[string]string{"a": "[01, 2, 03]"}},
	}
	for _, tc := range docs {
		t.Run(tc.doc, func(t *testing.T) {
			var std map[string]json.RawMessage
			require.Error(t, json.Unmarshal([]byte(tc.doc), &std), "strict JSON must reject %q", tc.doc)

			got, err := parseJSONObject([]byte(tc.doc))
			require.NoError(t, err)
			for k, want := range tc.want {
				require.Contains(t, got, k)
				assert.Equal(t, want, string(got[k]))
			}
		})
	}
}

// TestJSONScannerDepthLimit pins that absurd nesting is rejected rather than
// overflowing the stack, matching the encoding/json nesting limit.
func TestJSONScannerDepthLimit(t *testing.T) {
	deep := strings.Repeat("[", maxJSONDepth+1) + strings.Repeat("]", maxJSONDepth+1)
	_, err := parseJSONArray([]byte(deep))
	require.Error(t, err)

	var std any
	require.Error(t, json.Unmarshal([]byte(deep), &std), "encoding/json also rejects nesting beyond the limit")
}
