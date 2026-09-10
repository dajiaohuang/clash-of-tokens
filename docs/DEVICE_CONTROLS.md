# Device controls

The Devices page exposes a bounded, read-only doctor for the explicitly
configured physical Android device. Press **Check device** to call:

```text
POST /admin/device/check
Authorization: Bearer <admin key>
```

The endpoint requires the same-origin mutation policy as other admin actions
and uses the active runtime device settings. It returns the configuration
revision, whether device settings are waiting for restart, the doctor report,
and one row for each configured `app-device` source. The report includes:

- whether the configured ADB and OCR executable paths are usable;
- the configured serial and read-only `adb get-state` connection result;
- the active logical display resolution from `wm size`;
- the foreground Android package from `dumpsys window windows`;
- installation checks for the three supported app profiles.

`Ready` requires usable ADB/OCR paths, an online authorized device, a parsed
resolution, and a parsed foreground package. `LiveVerified` remains false for
the doctor. An app row reports `login_state: detected` and `last_test: pass`
only when the evidence store contains a verified validation for that source;
catalog metadata, package installation, and a successful ADB probe do not prove
a logged-in conversation. Without such evidence the state is `not_observed`
and `not_tested`.

The doctor never starts an emulator, launches an application, changes the
clipboard, navigates a conversation, logs in, or sends a message. A real
question remains an explicit `cot-device-verify -live` operation and is not
available from the management page.

The Go doctor and API tests use empty or synthetic configuration only. No
physical device, emulator, account, or provider credential is required for the
regression suite.
