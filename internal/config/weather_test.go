package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWeatherAreaIsCanonicalizedDefaultedAndValidated(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"", "City"}, {"  bedok ", "Bedok"}, {"TOA PAYOH", "Toa Payoh"}} {
		if got := normalizeWeatherArea(tc.in); got != tc.want {
			t.Errorf("normalizeWeatherArea(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if err := (Config{Weather: WeatherConfig{Area: "Atlantis"}}).validateWeather(); err == nil {
		t.Fatal("an unknown area validated")
	}
	if err := (Config{Weather: WeatherConfig{Area: "bedok"}}).validateWeather(); err != nil {
		t.Fatalf("a known area in another casing was refused: %v", err)
	}
}

func TestSetWeatherAreaWritesTheCanonicalName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetWeatherArea(path, "pasir ris"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil || cfg.Weather.Area != "Pasir Ris" {
		t.Fatalf("area = %q, err = %v", cfg.Weather.Area, err)
	}
	if err := SetWeatherArea(path, "Atlantis"); err == nil || !strings.Contains(err.Error(), "weather.area") {
		t.Fatalf("unknown area: err = %v", err)
	}
}
