# Log-Manager
```bash
go get github.com/Mexican-Man/log-manager
```

There are lots of good log rotators out there, but I couldn't find one that gave me the granularity/number of features that I needed. So, I've compiled features from other log rotating packages into this one. If you would think it's missing a feature, please open an issue or PR.

## How to Use
```go
import (
    lm "github.com/Mexican-Man/log-manager"
)

manager := lm.New(lm.LogManagerOptions{
    Dir:              "/path/to/logs",
    RotationInterval: time.Hour * 24,
}, time.Now().Add(time.Hour*24).Truncate(time.Hour*24))

log.SetOutput(manager)
```

## Options
- *`Dir` — Directory to store logs in
- *`RotationInterval` — How often to rotate logs (0 disables it)
- `FilenameFormat` — Template string using [text/template](https://pkg.go.dev/text/template) (more info below)
- `MaxFileSize` — How large a file can get before its rotated (0 for no limit)
- `GZIP` — GZIP old logs
- `LatestDotLog` — Keeps a symlink called `latest.log` that points to the latest log

## More Details
### `Filenameformat`
Here's the templated struct format:
```go
type LogTemplate struct {
	Time      time.Time
	Iteration uint
}
```

When rotating, `Interation` will increase if another log with the same name already exists. If increasing the iteration does not solve the issue, it will throw an error, and continue writing to the old log.

Here's an example:
```go
{{ .Time.Format "2006-01-02" }}{{ if .Iteration }}_{{ .Iteration }}{{ end }}
```
This will print create logs like this:
- 2022-05-17.log
- 2022-05-17_1.log
- 2022-05-18.log

> Note that the date format is the [Go's standard date formatting](https://pkg.go.dev/time#Time.Format).

### Scheduled Rotation
You can set `RotationInterval` to indicate when your logs should rotate. This does not affect when the next rotation will occur. You can change that in the `New(lm.LogTemplate{}, [offset_time])`. For example, a `RotationInterval` of
```go
time.Hour * 24
```
and a `offset_time` of
```go
time.Now().Add(time.Hour*24).Truncate(time.Hour*24)
```
(like in the example at the top) will ensure that logs are rotated every 24 hours, at midnight.

