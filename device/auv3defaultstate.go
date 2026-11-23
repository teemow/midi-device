package device

import (
	"encoding/base64"
	"fmt"
	"math"
	"sort"
	"unicode/utf8"
)

// This file is the user-defined default-state model: a persistent, per-audio-unit
// default fullState (AuStateDoc) the AUM author applies automatically to any node
// of that audio unit. It is a human-readable YAML container (config dir, rig-as-
// code) separate from the volatile probe dumps. See docs/auv3-state-authoring.md.
//
// The honest hybrid: our own plugins' state and a large class of third-party
// plugins (JUCE XML, JSON synths) are UTF-8 text a user/agent can read and edit;
// the rest (FabFilter, iSEM's ISEMPatch, etc.) is opaque binary that can only be
// round-tripped, so it is stored base64. Many plugins also keep settings as
// typed values beside (or instead of) a blob: numbers, switches and strings,
// such as DigiStix's volume, power and preset_name next to its seq bytes. Those
// are stored as the typed value itself. Some keep their whole patch in
// dictionaries and arrays nested in the state (Beepstreet's Sunrizer and
// Drambo under "instrument"); such a value is stored as an archive of its own.
// Capture classifies each fullState leaf into the richest round-trip-safe
// encoding via ClassifyStateValues.

// AUv3DefaultState is one audio unit's user-defined default fullState. It is
// matched to a session node by the component tuple (type+subtype+manufacturer);
// the filename is only a human handle. The identity tuple is NOT re-stored in
// the AuStateDoc here — aum.buildAuStateDoc re-derives it from the component.
type AUv3DefaultState struct {
	Component ProbeComponent        `yaml:"component"`
	Name      string                `yaml:"name,omitempty"`
	State     map[string]StateEntry `yaml:"state"`
}

// StateEntry is one fullState key's value. It is either bytes, in exactly one
// round-trip-safe encoding:
//
//   - Text: the value is exact UTF-8 (our own JSON config, a JUCE/synth XML or
//     JSON body, …). Stored verbatim; bytes = []byte(Text). When the on-disk
//     blob is a short binary header followed by a text body (e.g. JUCE's "VC2!"
//     prefix before the XML), Prefix holds the base64 of that leading header and
//     the bytes are Prefix-decoded || Text.
//   - Base64: opaque binary (FabFilter FFBS, iSEM ISEMPatch, compressed/bplist
//     state, …). bytes = base64-decode(Base64).
//
// or a typed value, the plist scalar the plugin stored under the key:
//
//   - String: a string (a preset name). Unlike Text it is not bytes: the
//     plugin reads it as a string.
//   - Bool: a switch.
//   - Int: an integer.
//   - Float: a 32-bit real, the Float most plugins store their settings as.
//   - Double: a 64-bit real.
//   - Archive: a nested value, dictionaries and arrays of the above, as the
//     base64 of its own NSKeyedArchiver binary plist: the canonical encoding
//     aum-session-go's aum.EncodeAuStateValue writes and aum.DecodeAuStateValue
//     reads (Value returns it as an ArchivedValue).
//
// Exactly one of them is set (validated); an entry with none is an empty blob.
// Prefix is only valid with Text. JSON and XML are stored as Text (they are
// UTF-8); structured field-level editing parses them on demand and is layered
// on top later — the storage model stays a byte-exact text/binary split so
// capture round-trips losslessly. The real's width is kept because the plist
// keeps it: a plugin reading its own Float gets a Float back.
type StateEntry struct {
	Text   string `yaml:"text,omitempty" json:"text,omitempty"`
	Prefix string `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	Base64 string `yaml:"base64,omitempty" json:"base64,omitempty"`

	String *string  `yaml:"string,omitempty" json:"string,omitempty"`
	Bool   *bool    `yaml:"bool,omitempty" json:"bool,omitempty"`
	Int    *int64   `yaml:"int,omitempty" json:"int,omitempty"`
	Float  *float32 `yaml:"float,omitempty" json:"float,omitempty"`
	Double *float64 `yaml:"double,omitempty" json:"double,omitempty"`

	Archive string `yaml:"archive,omitempty" json:"archive,omitempty"`
}

// ArchivedValue is a nested saved-state value, a dictionary or an array, as
// its own NSKeyedArchiver binary plist (aum.EncodeAuStateValue). It is a typed
// value, never bytes the plugin reads: a session writer decodes it
// (aum.DecodeAuStateValue) and writes the value it holds.
type ArchivedValue []byte

// archiveMagic starts every binary plist.
const archiveMagic = "bplist00"

// The encodings an entry can have, as Kind names them.
const (
	StateKindText       = "text"
	StateKindTextPrefix = "text+prefix"
	StateKindBase64     = "base64"
	StateKindString     = "string"
	StateKindBool       = "bool"
	StateKindInt        = "int"
	StateKindFloat      = "float"
	StateKindDouble     = "double"
	StateKindArchive    = "archive"
)

// kinds lists the encodings an entry sets, in field order. Text and Prefix
// count as one (text with a binary header).
func (e StateEntry) kinds() []string {
	var out []string
	switch {
	case e.Prefix != "":
		out = append(out, StateKindTextPrefix)
	case e.Text != "":
		out = append(out, StateKindText)
	}
	if e.Base64 != "" {
		out = append(out, StateKindBase64)
	}
	if e.String != nil {
		out = append(out, StateKindString)
	}
	if e.Bool != nil {
		out = append(out, StateKindBool)
	}
	if e.Int != nil {
		out = append(out, StateKindInt)
	}
	if e.Float != nil {
		out = append(out, StateKindFloat)
	}
	if e.Double != nil {
		out = append(out, StateKindDouble)
	}
	if e.Archive != "" {
		out = append(out, StateKindArchive)
	}
	return out
}

// Kind names the entry's encoding: one of the StateKind constants. An entry
// with no encoding is an empty blob, "text"; one with several (invalid) is
// named by the first.
func (e StateEntry) Kind() string {
	if k := e.kinds(); len(k) > 0 {
		return k[0]
	}
	return StateKindText
}

// IsValue reports whether the entry holds a typed value rather than bytes.
func (e StateEntry) IsValue() bool {
	return e.String != nil || e.Bool != nil || e.Int != nil || e.Float != nil || e.Double != nil || e.Archive != ""
}

// check validates that the entry uses at most one encoding.
func (e StateEntry) check() error {
	if k := e.kinds(); len(k) > 1 {
		return fmt.Errorf("state entry sets %v (use exactly one encoding)", k)
	}
	return nil
}

// Bytes resolves a single entry to its wire bytes, validating the encoding.
// An entry holding a typed value has no bytes: see Value.
func (e StateEntry) Bytes() ([]byte, error) {
	if err := e.check(); err != nil {
		return nil, err
	}
	switch {
	case e.IsValue():
		return nil, fmt.Errorf("state entry holds a %s value, not bytes", e.Kind())
	case e.Base64 != "":
		b, err := base64.StdEncoding.DecodeString(e.Base64)
		if err != nil {
			return nil, fmt.Errorf("state entry base64: %w", err)
		}
		return b, nil
	case e.Text == "" && e.Prefix == "":
		// An explicitly empty value is a legitimate empty blob.
		return []byte{}, nil
	default: // text (with optional binary prefix)
		var out []byte
		if e.Prefix != "" {
			p, err := base64.StdEncoding.DecodeString(e.Prefix)
			if err != nil {
				return nil, fmt.Errorf("state entry prefix: %w", err)
			}
			out = append(out, p...)
		}
		return append(out, []byte(e.Text)...), nil
	}
}

// Value resolves an entry that holds a typed value to that value: a string,
// bool, int64, float32 or float64, the types aum.NodeSpec.StateValues takes,
// or an ArchivedValue, which the session writer decodes first. An entry
// holding bytes has no typed value: see Bytes.
func (e StateEntry) Value() (any, error) {
	if err := e.check(); err != nil {
		return nil, err
	}
	switch {
	case e.String != nil:
		return *e.String, nil
	case e.Bool != nil:
		return *e.Bool, nil
	case e.Int != nil:
		return *e.Int, nil
	case e.Float != nil:
		if f := float64(*e.Float); math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("state entry float is not finite (%v)", f)
		}
		return *e.Float, nil
	case e.Double != nil:
		if f := *e.Double; math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("state entry double is not finite (%v)", f)
		}
		return *e.Double, nil
	case e.Archive != "":
		b, err := base64.StdEncoding.DecodeString(e.Archive)
		if err != nil {
			return nil, fmt.Errorf("state entry archive: %w", err)
		}
		if !hasPrefix(b, archiveMagic) {
			return nil, fmt.Errorf("state entry archive is not a binary plist")
		}
		return ArchivedValue(b), nil
	}
	return nil, fmt.Errorf("state entry holds %s bytes, not a typed value", e.Kind())
}

// NewStateValue is the entry holding a typed value: a string, bool, integer
// or finite real, as a session decodes them (int64 and uint64 for integers,
// float32 and float64 for reals), or a nested value as an ArchivedValue.
// Other values, such as a nested dictionary not yet archived or an integer
// beyond int64, have no entry and return an error.
func NewStateValue(v any) (StateEntry, error) {
	switch x := v.(type) {
	case ArchivedValue:
		if !hasPrefix(x, archiveMagic) {
			return StateEntry{}, fmt.Errorf("an archived value is not a binary plist")
		}
		return StateEntry{Archive: base64.StdEncoding.EncodeToString(x)}, nil
	case string:
		return StateEntry{String: &x}, nil
	case bool:
		return StateEntry{Bool: &x}, nil
	case int:
		return intEntry(int64(x)), nil
	case int8:
		return intEntry(int64(x)), nil
	case int16:
		return intEntry(int64(x)), nil
	case int32:
		return intEntry(int64(x)), nil
	case int64:
		return intEntry(x), nil
	case uint:
		return uintEntry(uint64(x))
	case uint8:
		return intEntry(int64(x)), nil
	case uint16:
		return intEntry(int64(x)), nil
	case uint32:
		return intEntry(int64(x)), nil
	case uint64:
		return uintEntry(x)
	case float32:
		if f := float64(x); math.IsNaN(f) || math.IsInf(f, 0) {
			return StateEntry{}, fmt.Errorf("float %v is not finite", x)
		}
		return StateEntry{Float: &x}, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return StateEntry{}, fmt.Errorf("double %v is not finite", x)
		}
		return StateEntry{Double: &x}, nil
	default:
		return StateEntry{}, fmt.Errorf("a %T is not a typed value a state entry holds (string, bool, integer or real)", v)
	}
}

func intEntry(i int64) StateEntry { return StateEntry{Int: &i} }

func uintEntry(u uint64) (StateEntry, error) {
	if u > math.MaxInt64 {
		return StateEntry{}, fmt.Errorf("integer %d is beyond int64", u)
	}
	return intEntry(int64(u)), nil
}

// StateDoc resolves every entry holding bytes, returning the fullState key ->
// bytes map aum.NodeSpec.StateDoc / SetAuStateDoc expect. Entries holding a
// typed value are left to StateValues. Keys are validated to be non-empty and
// to exclude the identity keys (which the author re-derives).
func (d AUv3DefaultState) StateDoc() (map[string][]byte, error) {
	out := make(map[string][]byte, len(d.State))
	for _, k := range sortedStateKeys(d.State) {
		if err := checkStateKey(k); err != nil {
			return nil, err
		}
		e := d.State[k]
		if e.IsValue() {
			continue
		}
		b, err := e.Bytes()
		if err != nil {
			return nil, fmt.Errorf("state[%q]: %w", k, err)
		}
		out[k] = b
	}
	return out, nil
}

// StateValues resolves every entry holding a typed value, returning the
// fullState key -> value map aum.NodeSpec.StateValues expects. Entries holding
// bytes are left to StateDoc; the two never share a key. Keys are validated as
// StateDoc validates them.
func (d AUv3DefaultState) StateValues() (map[string]any, error) {
	out := map[string]any{}
	for _, k := range sortedStateKeys(d.State) {
		if err := checkStateKey(k); err != nil {
			return nil, err
		}
		e := d.State[k]
		if !e.IsValue() {
			continue
		}
		v, err := e.Value()
		if err != nil {
			return nil, fmt.Errorf("state[%q]: %w", k, err)
		}
		out[k] = v
	}
	return out, nil
}

func checkStateKey(k string) error {
	if k == "" {
		return fmt.Errorf("state has an empty key")
	}
	switch k {
	case "type", "subtype", "manufacturer", "version":
		return fmt.Errorf("state key %q is an AuStateDoc identity key (re-derived from the component, do not store it)", k)
	}
	return nil
}

// Validate checks the component identity and that every entry resolves.
func (d AUv3DefaultState) Validate() error {
	if d.Component.Type == "" || d.Component.Subtype == "" || d.Component.Manufacturer == "" {
		return fmt.Errorf("component needs type, subtype and manufacturer")
	}
	if len(d.State) == 0 {
		return fmt.Errorf("state is empty (nothing to apply)")
	}
	if _, err := d.StateDoc(); err != nil {
		return err
	}
	_, err := d.StateValues()
	return err
}

// ClassifyStateValues turns a harvested fullState (key -> value, as a session
// decodes it: bytes or a typed value) into a map of StateEntry. Bytes are
// classified as ClassifyStateDoc classifies them; strings, switches, integers
// and reals are kept as the typed value (NewStateValue), and so is a nested
// value the caller archived first (an ArchivedValue). left are the keys whose
// values no entry can hold, sorted: nested dictionaries and lists not archived,
// and numbers a session cannot write (an integer beyond int64, a real that is
// not finite).
func ClassifyStateValues(vals map[string]any) (entries map[string]StateEntry, left []string) {
	entries = make(map[string]StateEntry, len(vals))
	for k, v := range vals {
		if b, ok := v.([]byte); ok {
			entries[k] = classifyStateEntry(b)
			continue
		}
		e, err := NewStateValue(v)
		if err != nil {
			left = append(left, k)
			continue
		}
		entries[k] = e
	}
	sort.Strings(left)
	return entries, left
}

// ClassifyStateDoc turns a harvested fullState (key -> raw bytes) into a map of
// StateEntry, picking the richest round-trip-safe encoding per leaf (Tier-0 text
// detection from docs/auv3-state-authoring.md):
//
//   - pure UTF-8 text -> Text
//   - a short binary header followed by an XML/text body -> Text + Prefix
//   - anything else (binary / compressed / bplist) -> Base64
//
// Every result is verified to round-trip back to the input bytes; a leaf that
// would not (an over-eager text split) falls back to Base64, so capture is
// always lossless.
func ClassifyStateDoc(doc map[string][]byte) map[string]StateEntry {
	out := make(map[string]StateEntry, len(doc))
	for k, b := range doc {
		out[k] = classifyStateEntry(b)
	}
	return out
}

// maxTextPrefix bounds how many leading binary bytes a text body may hide behind
// (e.g. JUCE's "VC2!" + a length word). Beyond this the blob is treated as binary.
const maxTextPrefix = 16

func classifyStateEntry(b []byte) StateEntry {
	if len(b) == 0 {
		return StateEntry{}
	}
	if isPrintableUTF8(b) {
		return StateEntry{Text: string(b)}
	}
	// A short binary header (length/version word) before an XML/text body: split
	// it off so the readable part is editable while the header round-trips.
	if i := textBodyStart(b); i > 0 {
		body := b[i:]
		if isPrintableUTF8(body) {
			return StateEntry{
				Prefix: base64.StdEncoding.EncodeToString(b[:i]),
				Text:   string(body),
			}
		}
	}
	return StateEntry{Base64: base64.StdEncoding.EncodeToString(b)}
}

// textBodyStart returns the index of an XML declaration / root element that
// begins within the first maxTextPrefix bytes (after a short binary header), or
// -1. It anchors on "<?xml" or "<<alpha" so it does not fire on stray '<' bytes
// inside binary.
func textBodyStart(b []byte) int {
	limit := maxTextPrefix
	if limit > len(b)-1 {
		limit = len(b) - 1
	}
	for i := 1; i <= limit; i++ {
		if b[i] != '<' {
			continue
		}
		rest := b[i:]
		if hasPrefix(rest, "<?xml") {
			return i
		}
		if len(rest) >= 2 && isAlpha(rest[1]) {
			return i
		}
	}
	return -1
}

// isPrintableUTF8 reports whether b is valid UTF-8 made only of printable runes
// and common whitespace — i.e. safe and useful to store as a YAML text scalar.
func isPrintableUTF8(b []byte) bool {
	if len(b) == 0 || !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func hasPrefix(b []byte, s string) bool {
	if len(b) < len(s) {
		return false
	}
	return string(b[:len(s)]) == s
}

func sortedStateKeys(m map[string]StateEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
