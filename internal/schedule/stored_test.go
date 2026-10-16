package schedule

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shyper/flimmerkiste/internal/rules"
)

var update = flag.Bool("update", false, "rewrite testdata/normalized")

// TestStoredConfigs reads configurations in the formats of older versions
// (see testdata/configs/README.md): they must stay valid, normalize to the
// reviewed result and stay stable when saved again.
func TestStoredConfigs(t *testing.T) {
	files, _ := filepath.Glob("testdata/configs/*.json")
	if len(files) == 0 {
		t.Fatal("no stored configs")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var in struct {
				Kind   string          `json:"kind"`
				Config json.RawMessage `json:"config"`
			}
			if err := json.Unmarshal(data, &in); err != nil {
				t.Fatal(err)
			}
			cfg, err := ParseConfig(in.Config)
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.Validate(in.Kind); err != nil {
				t.Fatalf("stored config no longer valid: %v", err)
			}
			out, _ := json.MarshalIndent(map[string]any{"kind": in.Kind, "config": cfg}, "", "  ")
			out = append(out, '\n')
			golden := filepath.Join("testdata/normalized", name+".json")
			if *update {
				if err := os.WriteFile(golden, out, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if !bytes.Equal(out, want) {
				t.Fatalf("normalized config changed; review and run with -update:\n%s", out)
			}
			// Saving the migrated config again must not change it.
			again, err := ParseConfig(mustJSON(cfg))
			if err != nil || !bytes.Equal(mustJSON(again), mustJSON(cfg)) {
				t.Fatal("normalizing is not stable")
			}
		})
	}
}

// TestRuleFields keeps the filter fields the editor tests render with in sync.
func TestRuleFields(t *testing.T) {
	out, _ := json.MarshalIndent(rules.Fields, "", "  ")
	out = append(out, '\n')
	golden := "testdata/normalized/fields.json"
	if *update {
		if err := os.WriteFile(golden, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if want, err := os.ReadFile(golden); err != nil || !bytes.Equal(out, want) {
		t.Fatal("filter fields changed; run with -update")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
