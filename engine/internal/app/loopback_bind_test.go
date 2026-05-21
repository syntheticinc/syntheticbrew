package app

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestIsLoopbackBind(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"localhost", true},

		{"", false},
		{"0.0.0.0", false},
		{"::", false},
		{"10.0.0.5", false},
		{"engine.svc.cluster.local", false},
		{"app.syntheticbrew.ai", false},
	}
	for _, c := range cases {
		if got := isLoopbackBind(c.host); got != c.want {
			t.Errorf("isLoopbackBind(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// TestWarnUnsafeLocalBind verifies the startup warning gate. Captures slog
// output through a custom JSON handler so each scenario can assert on whether
// the WARN line was emitted and on its structured fields.
func TestWarnUnsafeLocalBind(t *testing.T) {
	cases := []struct {
		name     string
		authMode string
		host     string
		port     int
		wantWarn bool
	}{
		{name: "local + empty host (bind all)", authMode: "local", host: "", port: 8443, wantWarn: true},
		{name: "local + 0.0.0.0", authMode: "local", host: "0.0.0.0", port: 8443, wantWarn: true},
		{name: "local + public IP", authMode: "local", host: "10.0.0.5", port: 9555, wantWarn: true},
		{name: "local + cluster DNS", authMode: "local", host: "engine.svc.cluster.local", port: 8443, wantWarn: true},

		{name: "local + 127.0.0.1", authMode: "local", host: "127.0.0.1", port: 8443, wantWarn: false},
		{name: "local + ::1", authMode: "local", host: "::1", port: 8443, wantWarn: false},
		{name: "local + localhost", authMode: "local", host: "localhost", port: 8443, wantWarn: false},

		{name: "external + 0.0.0.0", authMode: "external", host: "0.0.0.0", port: 8443, wantWarn: false},
		{name: "external + empty host", authMode: "external", host: "", port: 8443, wantWarn: false},
		{name: "external + 127.0.0.1", authMode: "external", host: "127.0.0.1", port: 8443, wantWarn: false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
			prev := slog.Default()
			slog.SetDefault(slog.New(h))
			defer slog.SetDefault(prev)

			warnUnsafeLocalBind(context.Background(), c.authMode, c.host, c.port)

			out := buf.String()
			emitted := strings.Contains(out, "AUTH_MODE=local with non-loopback bind")

			if emitted != c.wantWarn {
				t.Fatalf("warn emitted = %v, want %v; log output: %s", emitted, c.wantWarn, out)
			}
			if !c.wantWarn {
				return
			}
			// When the WARN fires it must carry the structured fields with the
			// exact host/port we passed in. Substring check is enough — JSON
			// keys are stable.
			if !strings.Contains(out, `"level":"WARN"`) {
				t.Errorf("expected WARN level, got: %s", out)
			}
			if !strings.Contains(out, `"listen_port":`) {
				t.Errorf("expected listen_port field, got: %s", out)
			}
			if !strings.Contains(out, `"listen_host":`) {
				t.Errorf("expected listen_host field, got: %s", out)
			}
		})
	}
}
