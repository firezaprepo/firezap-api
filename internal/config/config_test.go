package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(env(map[string]string{
		"FZ_ADMIN_KEY": strings.Repeat("k", 32),
		"DATABASE_URL": "postgres://u:p@localhost/db",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPPort != 8080 || cfg.DefaultLanguage != "en" || cfg.LogLevel != "info" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.WebhookTimeout != 10*time.Second || cfg.SendMinInterval != time.Second {
		t.Fatalf("unexpected durations: %+v", cfg)
	}
}

func TestLoadRequiresStrongAdminKeyAndDatabase(t *testing.T) {
	_, err := load(env(map[string]string{"FZ_ADMIN_KEY": "short"}))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"FZ_ADMIN_KEY", "DATABASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	_, err := load(env(map[string]string{
		"FZ_ADMIN_KEY":     strings.Repeat("k", 32),
		"DATABASE_URL":     "postgres://x",
		"HTTP_PORT":        "99999",
		"DEFAULT_LANGUAGE": "es",
		"LOG_LEVEL":        "loud",
	}))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"HTTP_PORT", "DEFAULT_LANGUAGE", "LOG_LEVEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

func TestWebhookAllowPrivate(t *testing.T) {
	base := map[string]string{"FZ_ADMIN_KEY": strings.Repeat("k", 32), "DATABASE_URL": "postgres://x"}
	cfg, err := load(env(base))
	if err != nil || cfg.WebhookAllowPrivate {
		t.Fatalf("must be off by default: %v %+v", err, cfg.WebhookAllowPrivate)
	}
	base["WEBHOOK_ALLOW_PRIVATE"] = "true"
	if cfg, err = load(env(base)); err != nil || !cfg.WebhookAllowPrivate {
		t.Fatalf("true: %v %v", err, cfg.WebhookAllowPrivate)
	}
	base["WEBHOOK_ALLOW_PRIVATE"] = "maybe"
	if _, err = load(env(base)); err == nil || !strings.Contains(err.Error(), "WEBHOOK_ALLOW_PRIVATE") {
		t.Fatalf("invalid value accepted: %v", err)
	}
}

func TestSendLimits(t *testing.T) {
	base := map[string]string{"FZ_ADMIN_KEY": strings.Repeat("k", 32), "DATABASE_URL": "postgres://x"}
	cfg, err := load(env(base))
	if err != nil || cfg.SendMaxPerMinute != 30 || cfg.SendMaxPerHour != 600 || cfg.SendQueueMax != 50 {
		t.Fatalf("defaults: %v %d/%d/%d", err, cfg.SendMaxPerMinute, cfg.SendMaxPerHour, cfg.SendQueueMax)
	}
	base["SEND_MAX_PER_MINUTE"], base["SEND_MAX_PER_HOUR"] = "0", "0" // 0 turns a limit off
	if cfg, err = load(env(base)); err != nil || cfg.SendMaxPerMinute != 0 || cfg.SendMaxPerHour != 0 {
		t.Fatalf("zero: %v %+v", err, cfg)
	}
	for key, bad := range map[string]string{"SEND_MAX_PER_MINUTE": "-1", "SEND_MAX_PER_HOUR": "many", "SEND_QUEUE_MAX": "0"} {
		b := map[string]string{"FZ_ADMIN_KEY": strings.Repeat("k", 32), "DATABASE_URL": "postgres://x", key: bad}
		if _, err = load(env(b)); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%q accepted: %v", key, bad, err)
		}
	}
}

func TestMediaMaxMB(t *testing.T) {
	base := map[string]string{"FZ_ADMIN_KEY": strings.Repeat("k", 32), "DATABASE_URL": "postgres://x"}
	cfg, err := load(env(base))
	if err != nil || cfg.MediaMaxBytes != 16<<20 {
		t.Fatalf("default must be 16 MB: %v %d", err, cfg.MediaMaxBytes)
	}
	base["MEDIA_MAX_MB"] = "64"
	if cfg, err = load(env(base)); err != nil || cfg.MediaMaxBytes != 64<<20 {
		t.Fatalf("64: %v %d", err, cfg.MediaMaxBytes)
	}
	for _, v := range []string{"0", "101", "lots"} {
		base["MEDIA_MAX_MB"] = v
		if _, err = load(env(base)); err == nil || !strings.Contains(err.Error(), "MEDIA_MAX_MB") {
			t.Errorf("%q accepted: %v", v, err)
		}
	}
}
