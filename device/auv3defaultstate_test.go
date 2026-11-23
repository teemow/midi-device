package device

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestClassifyStateDocRoundTrip(t *testing.T) {
	doc := map[string][]byte{
		"probeMidiBrainConfig": []byte(`{"host":"box:7800","controlEnabled":true}`),                            // pure JSON text
		"jucePluginState":      append([]byte("VC2!\x9c\x01\x00\x00"), []byte(`<?xml version="1.0"?><P/>`)...), // binary header + XML
		"ModulePreset":         []byte(`<ModulePreset><Script/></ModulePreset>`),                               // bare XML text
		"data":                 {0x00, 0x01, 0x02, 0xff, 0x80, 0x7f, 0x00},                                     // opaque binary
		"empty":                {},
	}
	got := ClassifyStateDoc(doc)

	if e := got["probeMidiBrainConfig"]; e.Text == "" || e.Base64 != "" || e.Prefix != "" {
		t.Errorf("JSON should classify as text, got %+v", e)
	}
	if e := got["jucePluginState"]; e.Text == "" || e.Prefix == "" {
		t.Errorf("prefixed XML should classify as text+prefix, got %+v", e)
	} else if e.Text[:5] != "<?xml" {
		t.Errorf("prefixed XML text should start at the XML decl, got %q", e.Text[:5])
	}
	if e := got["ModulePreset"]; e.Text == "" || e.Prefix != "" {
		t.Errorf("bare XML should classify as text (no prefix), got %+v", e)
	}
	if e := got["data"]; e.Base64 == "" || e.Text != "" {
		t.Errorf("opaque binary should classify as base64, got %+v", e)
	}

	// Every classified entry must round-trip back to the exact input bytes.
	d := AUv3DefaultState{
		Component: ProbeComponent{Type: "aumu", Subtype: "iSEM", Manufacturer: "Artu"},
		State:     got,
	}
	rt, err := d.StateDoc()
	if err != nil {
		t.Fatalf("StateDoc: %v", err)
	}
	for k, want := range doc {
		if !bytes.Equal(rt[k], want) {
			t.Errorf("round-trip %q = %x, want %x", k, rt[k], want)
		}
	}
}

func TestStateEntryBytes(t *testing.T) {
	raw := []byte{0xde, 0xad, 0xbe, 0xef}
	cases := map[string]struct {
		e    StateEntry
		want []byte
		err  bool
	}{
		"text":        {StateEntry{Text: "hi"}, []byte("hi"), false},
		"base64":      {StateEntry{Base64: base64.StdEncoding.EncodeToString(raw)}, raw, false},
		"prefix+text": {StateEntry{Prefix: base64.StdEncoding.EncodeToString([]byte("AB")), Text: "<x/>"}, []byte("AB<x/>"), false},
		"empty":       {StateEntry{}, []byte{}, false},
		"both":        {StateEntry{Text: "x", Base64: "eA=="}, nil, true},
		"bad base64":  {StateEntry{Base64: "!!!notb64"}, nil, true},
		"bad prefix":  {StateEntry{Prefix: "!!!", Text: "x"}, nil, true},
	}
	for name, c := range cases {
		got, err := c.e.Bytes()
		if c.err {
			if err == nil {
				t.Errorf("%s: expected error", name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !bytes.Equal(got, c.want) {
			t.Errorf("%s: got %x want %x", name, got, c.want)
		}
	}
}

func TestStateDocRejectsIdentityKeys(t *testing.T) {
	d := AUv3DefaultState{
		Component: ProbeComponent{Type: "aumu", Subtype: "iSEM", Manufacturer: "Artu"},
		State:     map[string]StateEntry{"type": {Text: "aumu"}},
	}
	if _, err := d.StateDoc(); err == nil {
		t.Fatal("expected identity key to be rejected")
	}
}

func TestAUv3DefaultStateValidate(t *testing.T) {
	ok := AUv3DefaultState{
		Component: ProbeComponent{Type: "aumu", Subtype: "iSEM", Manufacturer: "Artu"},
		State:     map[string]StateEntry{"data": {Base64: "AAAA"}},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}
	noComp := AUv3DefaultState{State: map[string]StateEntry{"data": {Text: "x"}}}
	if err := noComp.Validate(); err == nil {
		t.Fatal("missing component should fail")
	}
	noState := AUv3DefaultState{Component: ok.Component}
	if err := noState.Validate(); err == nil {
		t.Fatal("empty state should fail")
	}
}

func TestAUv3DefaultStateYAMLRoundTrip(t *testing.T) {
	in := AUv3DefaultState{
		Component: ProbeComponent{Type: "aumu", Subtype: "iSEM", Manufacturer: "Artu"},
		Name:      "Arturia iSEM",
		State: map[string]StateEntry{
			"probeMidiBrainConfig": {Text: `{"host":"box:7800"}`},
			"ISEMPatch":            {Base64: "AAAA"},
			"jucePluginState":      {Prefix: "VkMyIQ==", Text: "<?xml?>"},
		},
	}
	out, err := yaml.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back AUv3DefaultState
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	a, _ := in.StateDoc()
	b, err := back.StateDoc()
	if err != nil {
		t.Fatalf("StateDoc after round-trip: %v", err)
	}
	for k, av := range a {
		if !bytes.Equal(av, b[k]) {
			t.Errorf("yaml round-trip %q changed bytes", k)
		}
	}
	if back.Name != in.Name || back.Component.Subtype != "iSEM" {
		t.Errorf("yaml round-trip lost component/name: %+v", back)
	}
}

func TestStateEntryValue(t *testing.T) {
	s, b, i, f, d := "Classic 80's Kit 1", false, int64(-3), float32(55.600006), 0.25
	cases := map[string]struct {
		e    StateEntry
		want any
		err  bool
	}{
		"string":       {StateEntry{String: &s}, s, false},
		"bool":         {StateEntry{Bool: &b}, false, false},
		"int":          {StateEntry{Int: &i}, int64(-3), false},
		"float":        {StateEntry{Float: &f}, float32(55.600006), false},
		"double":       {StateEntry{Double: &d}, 0.25, false},
		"bytes":        {StateEntry{Text: "x"}, nil, true},
		"empty blob":   {StateEntry{}, nil, true},
		"two values":   {StateEntry{Int: &i, Bool: &b}, nil, true},
		"value+text":   {StateEntry{Text: "x", Float: &f}, nil, true},
		"nan float":    {StateEntry{Float: ptr(float32(math.NaN()))}, nil, true},
		"inf double":   {StateEntry{Double: ptr(math.Inf(1))}, nil, true},
		"empty string": {StateEntry{String: ptr("")}, "", false},
	}
	for name, c := range cases {
		got, err := c.e.Value()
		if c.err {
			if err == nil {
				t.Errorf("%s: expected error, got %#v", name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %#v want %#v", name, got, c.want)
		}
	}
	if _, err := (StateEntry{Int: &i}).Bytes(); err == nil {
		t.Error("Bytes of a typed value should fail")
	}
	if _, err := (StateEntry{Base64: "AAAA", Bool: &b}).Bytes(); err == nil {
		t.Error("Bytes of an entry with bytes and a value should fail")
	}
}

func TestStateEntryKind(t *testing.T) {
	i := int64(1)
	cases := map[string]StateEntry{
		StateKindText:       {Text: "x"},
		StateKindTextPrefix: {Prefix: "AA==", Text: "<x/>"},
		StateKindBase64:     {Base64: "AAAA"},
		StateKindString:     {String: ptr("x")},
		StateKindBool:       {Bool: ptr(true)},
		StateKindInt:        {Int: &i},
		StateKindFloat:      {Float: ptr(float32(1))},
		StateKindDouble:     {Double: ptr(1.0)},
		StateKindArchive:    {Archive: "YnBsaXN0MDA="},
	}
	for want, e := range cases {
		if got := e.Kind(); got != want {
			t.Errorf("Kind(%+v) = %q, want %q", e, got, want)
		}
	}
	if got := (StateEntry{}).Kind(); got != StateKindText {
		t.Errorf("an empty blob is %q, want text", got)
	}
}

// digiStixState is the shape of DigiStix's saved state: one sequence blob
// beside typed settings.
func digiStixState() map[string]any {
	return map[string]any{
		"seq":           []byte{0x00, 0x01, 0xfe, 0xff},
		"volume":        float32(12),
		"high_pass":     float32(55.600006),
		"power":         uint64(1),
		"delay_sync":    uint64(4),
		"preset_name":   "Classic 80's Kit 1",
		"notes":         "",
		"reverb_enable": true,
		"song_mode":     false,
		"offset":        int64(-7),
		"gain":          0.1,
		"grid":          map[string]any{"NS.keys": []any{}},
		"lanes":         []any{uint64(1)},
		"huge":          uint64(math.MaxUint64),
		"broken":        float32(math.Inf(-1)),
	}
}

func TestClassifyStateValues(t *testing.T) {
	in := digiStixState()
	entries, left := ClassifyStateValues(in)
	if want := []string{"broken", "grid", "huge", "lanes"}; !slices.Equal(left, want) {
		t.Errorf("left = %v, want %v", left, want)
	}
	kinds := map[string]string{
		"seq": StateKindBase64, "volume": StateKindFloat, "high_pass": StateKindFloat,
		"power": StateKindInt, "delay_sync": StateKindInt, "preset_name": StateKindString,
		"notes": StateKindString, "reverb_enable": StateKindBool, "song_mode": StateKindBool,
		"offset": StateKindInt, "gain": StateKindDouble,
	}
	for k, want := range kinds {
		if got := entries[k].Kind(); got != want {
			t.Errorf("%s classified as %q, want %q", k, got, want)
		}
	}
	if len(entries) != len(kinds) {
		t.Errorf("%d entries, want %d", len(entries), len(kinds))
	}

	d := AUv3DefaultState{Component: ProbeComponent{Type: "aumu", Subtype: "dmfx", Manufacturer: "FPKT"}, State: entries}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	doc, err := d.StateDoc()
	if err != nil {
		t.Fatalf("StateDoc: %v", err)
	}
	if len(doc) != 1 || !bytes.Equal(doc["seq"], in["seq"].([]byte)) {
		t.Errorf("StateDoc = %v, want only seq", doc)
	}
	vals, err := d.StateValues()
	if err != nil {
		t.Fatalf("StateValues: %v", err)
	}
	want := map[string]any{
		"volume": float32(12), "high_pass": float32(55.600006), "power": int64(1), "delay_sync": int64(4),
		"preset_name": "Classic 80's Kit 1", "notes": "", "reverb_enable": true, "song_mode": false,
		"offset": int64(-7), "gain": 0.1,
	}
	if !maps.Equal(vals, want) {
		t.Errorf("StateValues = %#v\nwant %#v", vals, want)
	}
}

func TestStateValuesRejectIdentityKeys(t *testing.T) {
	d := AUv3DefaultState{
		Component: ProbeComponent{Type: "aumu", Subtype: "dmfx", Manufacturer: "FPKT"},
		State:     map[string]StateEntry{"version": {Int: ptr(int64(1))}},
	}
	if _, err := d.StateValues(); err == nil {
		t.Fatal("expected identity key to be rejected")
	}
	if err := d.Validate(); err == nil {
		t.Fatal("Validate should reject an identity key holding a value")
	}
}

func TestAUv3DefaultStateValidateTypedValues(t *testing.T) {
	c := ProbeComponent{Type: "aumu", Subtype: "dmfx", Manufacturer: "FPKT"}
	only := AUv3DefaultState{Component: c, State: map[string]StateEntry{"power": {Int: ptr(int64(1))}}}
	if err := only.Validate(); err != nil {
		t.Errorf("a state of typed values only is valid: %v", err)
	}
	nan := AUv3DefaultState{Component: c, State: map[string]StateEntry{"volume": {Float: ptr(float32(math.NaN()))}}}
	if err := nan.Validate(); err == nil {
		t.Error("a non-finite float should not validate")
	}
	mixed := AUv3DefaultState{Component: c, State: map[string]StateEntry{"volume": {Text: "1", Float: ptr(float32(1))}}}
	if err := mixed.Validate(); err == nil {
		t.Error("an entry with bytes and a value should not validate")
	}
}

func TestTypedStateYAMLAndJSONRoundTrip(t *testing.T) {
	entries, _ := ClassifyStateValues(digiStixState())
	in := AUv3DefaultState{Component: ProbeComponent{Type: "aumu", Subtype: "dmfx", Manufacturer: "FPKT"}, Name: "DigiStix", State: entries}
	wantVals, err := in.StateValues()
	if err != nil {
		t.Fatal(err)
	}
	wantDoc, _ := in.StateDoc()

	out, err := yaml.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back AUv3DefaultState
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	gotVals, err := back.StateValues()
	if err != nil {
		t.Fatalf("StateValues after yaml: %v\n%s", err, out)
	}
	if !maps.Equal(gotVals, wantVals) {
		t.Errorf("yaml round trip changed values:\n got %#v\nwant %#v\n%s", gotVals, wantVals, out)
	}
	gotDoc, _ := back.StateDoc()
	if !maps.EqualFunc(gotDoc, wantDoc, bytes.Equal) {
		t.Errorf("yaml round trip changed bytes")
	}

	js, err := json.Marshal(in.State)
	if err != nil {
		t.Fatal(err)
	}
	var jback map[string]StateEntry
	if err := json.Unmarshal(js, &jback); err != nil {
		t.Fatal(err)
	}
	gotVals, err = AUv3DefaultState{State: jback}.StateValues()
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(gotVals, wantVals) {
		t.Errorf("json round trip changed values:\n got %#v\nwant %#v\n%s", gotVals, wantVals, js)
	}
}

// TestFloatStateYAMLRoundTripIsExact checks that a 32-bit real comes back
// from YAML with the same bits, so a captured setting is written back as the
// plugin saved it.
func TestFloatStateYAMLRoundTripIsExact(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for n := 0; n < 20000; n++ {
		f := math.Float32frombits(r.Uint32())
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			continue
		}
		d := math.Float64frombits(r.Uint64())
		if math.IsNaN(d) || math.IsInf(d, 0) {
			d = 0
		}
		out, err := yaml.Marshal(map[string]StateEntry{"f": {Float: &f}, "d": {Double: &d}})
		if err != nil {
			t.Fatal(err)
		}
		var back map[string]StateEntry
		if err := yaml.Unmarshal(out, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", out, err)
		}
		if g := back["f"].Float; g == nil || math.Float32bits(*g) != math.Float32bits(f) {
			t.Fatalf("float %v (%08x) came back as %v from %s", f, math.Float32bits(f), g, out)
		}
		if g := back["d"].Double; g == nil || math.Float64bits(*g) != math.Float64bits(d) {
			t.Fatalf("double %v came back as %v from %s", d, g, out)
		}
	}
}

// sunrizerPatch stands for a nested value's archive, as aum.EncodeAuStateValue
// writes it: a binary plist (this package only checks its magic).
var sunrizerPatch = ArchivedValue("bplist00\xd4\x01\x02\x03\x04patch")

// TestArchivedStateValue keeps a nested value as its archive: classified,
// stored in YAML and JSON, and resolved back to the same ArchivedValue, which
// StateValues hands on as a typed value, never as bytes.
func TestArchivedStateValue(t *testing.T) {
	vals := map[string]any{
		"instrument": sunrizerPatch,
		"data":       []byte{0, 0, 0, 0, 0xff},
		"nested":     map[string]any{"name": "not archived"},
	}
	entries, left := ClassifyStateValues(vals)
	if !slices.Equal(left, []string{"nested"}) {
		t.Fatalf("left = %v, want only the value not archived", left)
	}
	e := entries["instrument"]
	if e.Kind() != StateKindArchive || !e.IsValue() || e.Archive != base64.StdEncoding.EncodeToString(sunrizerPatch) {
		t.Fatalf("instrument entry = %+v (%s)", e, e.Kind())
	}
	if _, err := e.Bytes(); err == nil {
		t.Error("Bytes of an archive should fail: it is not bytes the plugin reads")
	}

	def := AUv3DefaultState{Component: ProbeComponent{Type: "aumu", Subtype: "bpsn", Manufacturer: "BPST"}, Name: "BPST: Sunrizer", State: entries}
	if err := def.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, codec := range map[string]struct {
		marshal   func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{"yaml": {yaml.Marshal, yaml.Unmarshal}, "json": {json.Marshal, json.Unmarshal}} {
		out, err := codec.marshal(def)
		if err != nil {
			t.Fatal(err)
		}
		var back AUv3DefaultState
		if err := codec.unmarshal(out, &back); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := back.StateValues()
		if err != nil {
			t.Fatalf("%s: StateValues: %v", name, err)
		}
		if a, ok := got["instrument"].(ArchivedValue); !ok || !bytes.Equal(a, sunrizerPatch) {
			t.Fatalf("%s: instrument resolved to %#v", name, got["instrument"])
		}
		doc, err := back.StateDoc()
		if err != nil || len(doc) != 1 || !bytes.Equal(doc["data"], vals["data"].([]byte)) {
			t.Fatalf("%s: StateDoc = %v (%v), want the data key alone", name, doc, err)
		}
	}

	bad := map[string]StateEntry{
		"not base64":     {Archive: "%%%"},
		"not a plist":    {Archive: base64.StdEncoding.EncodeToString([]byte("plain bytes"))},
		"archive+string": {Archive: e.Archive, String: ptr("x")},
	}
	for name, b := range bad {
		if _, err := b.Value(); err == nil {
			t.Errorf("%s: Value accepted %+v", name, b)
		}
	}
	if _, err := NewStateValue(ArchivedValue("plain bytes")); err == nil {
		t.Error("NewStateValue took an archived value that is not a binary plist")
	}
}

func ptr[T any](v T) *T { return &v }
