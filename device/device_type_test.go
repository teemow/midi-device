package device

import (
	"fmt"
	"testing"
)

func TestDefinitionValidateID(t *testing.T) {
	good := []string{"md-200", "eq2", "fabfilter_pro_q", "x32", "a"}
	bad := []string{"", "../etc/passwd", "a/b", "a..b", "..", "Up", "a b", "a.b", "-leading"}
	mk := func(id string) DeviceType {
		cc := 1
		return DeviceType{ID: id, Transport: "blemidi", Controls: []Control{
			{Name: "c", Type: ControlCC, CC: &cc, Value: ValueSpec{Type: ValueRange}},
		}}
	}
	for _, id := range good {
		d := mk(id)
		if err := d.Validate(); err != nil {
			t.Errorf("id %q: unexpected error %v", id, err)
		}
	}
	for _, id := range bad {
		d := mk(id)
		if err := d.Validate(); err == nil {
			t.Errorf("id %q: expected a validation error, got nil", id)
		}
	}
}

func TestDefinitionValidateAddressing(t *testing.T) {
	cc := func(n int) *int { return &n }
	bound := func(v float64) *float64 { return &v }

	cases := []struct {
		name    string
		def     DeviceType
		wantErr bool
	}{
		{
			name: "good cc",
			def: DeviceType{ID: "d", Transport: "blemidi", Controls: []Control{
				{Name: "level", Type: ControlCC, CC: cc(17), Value: ValueSpec{Type: ValueRange}},
			}},
		},
		{
			name: "cc missing number",
			def: DeviceType{ID: "d", Transport: "blemidi", Controls: []Control{
				{Name: "level", Type: ControlCC, Value: ValueSpec{Type: ValueRange}},
			}},
			wantErr: true,
		},
		{
			name: "parametric cc needs no number",
			def: DeviceType{ID: "d", Transport: "blemidi", Controls: []Control{
				{Name: "cc", Type: ControlCC, Parametric: true, Value: ValueSpec{Type: ValueRange}},
			}},
		},
		{
			name: "osc missing address",
			def: DeviceType{ID: "d", Transport: "osc", Controls: []Control{
				{Name: "fader", Type: ControlOSC, Value: ValueSpec{Type: ValueFloat}},
			}},
			wantErr: true,
		},
		{
			name: "sysex missing template",
			def: DeviceType{ID: "d", Transport: "blemidi", Controls: []Control{
				{Name: "x", Type: ControlSysEx, Value: ValueSpec{Type: ValueRange}},
			}},
			wantErr: true,
		},
		{
			name: "enum without values",
			def: DeviceType{ID: "d", Transport: "blemidi", Controls: []Control{
				{Name: "sw", Type: ControlCC, CC: cc(1), Value: ValueSpec{Type: ValueEnum}},
			}},
			wantErr: true,
		},
		{
			name: "min greater than max",
			def: DeviceType{ID: "d", Transport: "blemidi", Controls: []Control{
				{Name: "x", Type: ControlCC, CC: cc(1), Value: ValueSpec{Type: ValueRange, Min: bound(100), Max: bound(10)}},
			}},
			wantErr: true,
		},
		{
			name: "unknown control type",
			def: DeviceType{ID: "d", Transport: "blemidi", Controls: []Control{
				{Name: "x", Type: "weird", Value: ValueSpec{Type: ValueRange}},
			}},
			wantErr: true,
		},
		{
			name:    "missing transport",
			def:     DeviceType{ID: "d"},
			wantErr: true,
		},
		{
			name: "duplicate control names",
			def: DeviceType{ID: "d", Transport: "blemidi", Controls: []Control{
				{Name: "x", Type: ControlCC, CC: cc(1), Value: ValueSpec{Type: ValueRange}},
				{Name: "x", Type: ControlCC, CC: cc(2), Value: ValueSpec{Type: ValueRange}},
			}},
			wantErr: true,
		},
		{
			name: "pinned channel in range",
			def: DeviceType{ID: "d", Transport: "blemidi", Controls: []Control{
				{Name: "x", Type: ControlCC, CC: cc(1), Channel: cc(16), Value: ValueSpec{Type: ValueRange}},
			}},
		},
		{
			name: "pinned channel below range",
			def: DeviceType{ID: "d", Transport: "blemidi", Controls: []Control{
				{Name: "x", Type: ControlCC, CC: cc(1), Channel: cc(0), Value: ValueSpec{Type: ValueRange}},
			}},
			wantErr: true,
		},
		{
			name: "pinned channel above range",
			def: DeviceType{ID: "d", Transport: "blemidi", Controls: []Control{
				{Name: "x", Type: ControlCC, CC: cc(1), Channel: cc(17), Value: ValueSpec{Type: ValueRange}},
			}},
			wantErr: true,
		},
		{
			name: "sysex cannot pin a channel",
			def: DeviceType{ID: "d", Transport: "blemidi", Controls: []Control{
				{Name: "x", Type: ControlSysEx, SysEx: "F0 7D %v F7", Channel: cc(2), Value: ValueSpec{Type: ValueRange}},
			}},
			wantErr: true,
		},
		{
			name: "osc cannot pin a channel",
			def: DeviceType{ID: "d", Transport: "osc", Controls: []Control{
				{Name: "x", Type: ControlOSC, Address: "/a", Channel: cc(2), Value: ValueSpec{Type: ValueFloat}},
			}},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.def.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestControlWireChannel(t *testing.T) {
	cc := 1
	unpinned := Control{Type: ControlCC, CC: &cc}
	if got := unpinned.WireChannel(4); got != 4 {
		t.Fatalf("unpinned WireChannel(4) = %d, want the binding channel 4", got)
	}
	pin := 16 // 1-based in the definition -> 15 on the wire
	pinned := Control{Type: ControlCC, CC: &cc, Channel: &pin}
	if got := pinned.WireChannel(4); got != 15 {
		t.Fatalf("pinned WireChannel(4) = %d, want 15", got)
	}
}

func TestRegistryAddDefinition(t *testing.T) {
	r := NewRegistry()

	cc := 17
	good := &DeviceType{ID: "newdev", Name: "New Device", Transport: "blemidi", Controls: []Control{
		{Name: "level", Type: ControlCC, CC: &cc, Value: ValueSpec{Type: ValueRange}},
	}}
	if err := r.AddDefinition(good); err != nil {
		t.Fatalf("add good: %v", err)
	}
	got, ok := r.Get("newdev")
	if !ok || got.Name != "New Device" {
		t.Fatalf("round-trip failed: %+v ok=%v", got, ok)
	}

	// Invalid definitions are rejected and not inserted.
	bad := &DeviceType{ID: "baddev", Transport: "blemidi", Controls: []Control{
		{Name: "x", Type: ControlCC, Value: ValueSpec{Type: ValueRange}}, // cc missing
	}}
	if err := r.AddDefinition(bad); err == nil {
		t.Fatal("expected AddDefinition to reject an invalid definition")
	}
	if _, ok := r.Get("baddev"); ok {
		t.Fatal("invalid definition should not have been inserted")
	}
}

// TestBundledBattalion checks the shipped Battalion type against the plugin's
// MIDI layout, which is fixed by channel: the voices on channel 1, one voice a
// channel played at a pitch on 2 to 9, the sequencer and mixer on 10, presets
// and randomisers on 11.
func TestBundledBattalion(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("load bundled: %v", err)
	}
	d, ok := reg.Get("battalion")
	if !ok {
		t.Fatal("no bundled definition \"battalion\"")
	}
	if d.Transport != "auv3midi" {
		t.Errorf("transport = %q, want auv3midi", d.Transport)
	}
	byName := map[string]Control{}
	for _, c := range d.Controls {
		if c.Type != ControlNoteOn {
			t.Errorf("%s is a %s: the plugin has no controller map, every control is a note", c.Name, c.Type)
		}
		if c.Channel == nil {
			t.Errorf("%s has no channel of its own, and the plugin's layout is by channel", c.Name)
		}
		byName[c.Name] = c
	}
	fixed := []struct {
		name          string
		note, channel int
	}{
		{"voice_1", 36, 1}, {"voice_8", 43, 1},
		{"sequencer_toggle", 0, 10}, {"sequencer_play", 1, 10}, {"sequencer_stop", 2, 10},
		{"mute_1", 12, 10}, {"mute_8", 19, 10}, {"solo_1", 24, 10}, {"choke_8", 67, 10},
		{"preset_previous", 0, 11}, {"preset_next", 1, 11}, {"preset_random", 2, 11},
		{"randomize_all", 3, 11}, {"performance_commit", 14, 11}, {"randomize_voice_8", 31, 11},
	}
	for _, f := range fixed {
		c, ok := byName[f.name]
		switch {
		case !ok:
			t.Errorf("no control %s", f.name)
		case c.CC == nil || *c.CC != f.note || *c.Channel != f.channel:
			t.Errorf("%s = note %v on channel %d, want note %d on channel %d", f.name, c.CC, *c.Channel, f.note, f.channel)
		}
	}
	// A voice played at a pitch takes its note at the call, on the voice's
	// channel.
	for v := 1; v <= 8; v++ {
		c, ok := byName[fmt.Sprintf("play_voice_%d", v)]
		if !ok || !c.Parametric || *c.Channel != v+1 {
			t.Errorf("play_voice_%d = %+v, want a parametric note on channel %d", v, c, v+1)
		}
	}
	if len(d.Controls) != 58 {
		t.Errorf("%d controls, want 58", len(d.Controls))
	}
}
