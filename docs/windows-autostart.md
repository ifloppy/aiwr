# Windows: Auto-start the service at login

Run the service silently in the background on every login, without a terminal window or manual start.

## 1. Install the binary somewhere permanent

Install the Windows `aiwr.exe` release binary somewhere permanent (for example
`C:\aiwr\aiwr.exe`) and use that path consistently below. This setup runs the
native Go service; it does not require Python or the source checkout.

If you are developing from a checkout, build it first with `go build -o aiwr.exe
./cmd/aiwr` and use the resulting executable path instead.

## 2. Create a silent launcher script

Save as `C:\aiwr\start-service.vbs`:

```vbscript
Set WshShell = CreateObject("WScript.Shell")
WshShell.Run "C:\aiwr\aiwr.exe serve --host 127.0.0.1 --port 8765", 0, False
```

## 3. Register a scheduled task

This does **not** require Administrator privileges — `-AtLogOn` with a user-level trigger runs under your own account, so a regular PowerShell window is enough.

```powershell
$action = New-ScheduledTaskAction -Execute "wscript.exe" -Argument '"C:\aiwr\start-service.vbs"' -WorkingDirectory "C:\aiwr"
$trigger = New-ScheduledTaskTrigger -AtLogOn
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable
Register-ScheduledTask -TaskName "AiwrService" -Action $action -Trigger $trigger -Settings $settings -Description "Auto-starts the aiwr HTTP service at login"
```

## 4. Start it immediately (no reboot needed)

```powershell
Start-ScheduledTask -TaskName "AiwrService"
```

## 5. Verify

```powershell
Invoke-RestMethod http://127.0.0.1:8765/health
```

Should return `{"ok": true, "version": "..."}`.

## Notes

- Requires the native `aiwr.exe` binary; Python is not needed.
- The scheduled task runs at every login going forward — no manual start needed.
- To stop auto-starting: `Unregister-ScheduledTask -TaskName "AiwrService"`
