package config

import "testing"

func TestParseMemoryMiB(t *testing.T) {
	tests := map[string]uint64{"1M": 1, "16G": 16384, "2T": 2097152, "1024K": 1}
	for input, want := range tests {
		got, err := ParseMemoryMiB(input)
		if err != nil || got != want {
			t.Fatalf("ParseMemoryMiB(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"", "0G", "1GB", "1.5G"} {
		if _, err := ParseMemoryMiB(input); err == nil {
			t.Errorf("ParseMemoryMiB(%q) unexpectedly succeeded", input)
		}
	}
}

func TestParseWalltimeMinutes(t *testing.T) {
	tests := map[string]uint32{"00:00:01": 1, "01:00:00": 60, "2-00:00:00": 2880}
	for input, want := range tests {
		got, err := ParseWalltimeMinutes(input)
		if err != nil || got != want {
			t.Fatalf("ParseWalltimeMinutes(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := t.TempDir() + "/config.yaml"
	data := []byte("apiVersion: slurm-gha/v1alpha1\nunknown: true\n")
	if err := writeTestFile(path, data); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load unexpectedly accepted an unknown field")
	}
}
