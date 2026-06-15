# midi-device

Declarative device models for MIDI/USB control surfaces and effects. This is the
kernel extracted from [mcp-midi-controller](https://github.com/teemow/mcp-midi-controller):
a device-type registry, the bundled device definitions (Behringer X32, AUv3
plugins, USB editor devices), and the USB editor/readback wire codecs.

No MIDI IO lives here — this module is pure modeling. Pair it with a transport
module to actually drive hardware, and bring your own control loop.

## Install

```
go get github.com/teemow/midi-device/device
```

## Usage

```go
import "github.com/teemow/midi-device/device"

reg, _ := device.LoadBundled()        // ships x32, AUv3 plugins, USB editors
_ = reg.LoadDir("/etc/myapp/devices") // user adds YAML, overrides by id
dt, _ := reg.Get("x32")
```

Add device support with zero Go: drop a `*.yaml` device type in a directory and
`LoadDir` it. A user file overrides a bundled type by its `id:` field, not its
filename.

## Packages

- `device` — the `DeviceType` model (`Validate`, `Control`, `ControlNames`) and
  the `Registry` (`NewRegistry`, `LoadBundled`, `LoadDir`, `AddDefinition`,
  `Remove`, `Get`, `All`; concurrency-safe).
- `device/usbcodec` — the `Codec` interface and `New(protocol, Config)` factory
  for USB editor/readback protocols (Roland address-SysEx, Morningstar SysEx,
  Source Audio Neuro HID, Two Notes Torpedo HID).
- `device/sanitize` — `ID(s string) string`, the id normalizer the device and
  AUM session tooling share.

## License

See [LICENSE](LICENSE).
