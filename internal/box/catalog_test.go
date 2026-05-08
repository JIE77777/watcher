package box

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogProviderLoadsFixtureBox(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "models.fixture.json"), []byte(`{
  "datasets": {
    "models": {
      "records": [
        {"record_id":"m1","title":"Model 1","data":{"rank":2,"model":"Model 1","score":10,"category":"coding"}},
        {"record_id":"m2","title":"Model 2","data":{"rank":1,"model":"Model 2","score":20,"category":"coding"}}
      ]
    }
  }
}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "llm.box.json"), []byte(`{
  "id": "llm",
  "title": "LLM",
  "sources": [{"id":"fixture","kind":"fixture_json","path":"models.fixture.json"}],
  "datasets": [{"id":"models","source_id":"fixture","source_dataset":"models","view_id":"rank","group_by":"category"}],
  "views": [{"view_id":"rank","type":"ranking","title":"Rank","dataset_id":"models","group_by":"category","columns":[{"field":"model","label":"Model"},{"field":"score","label":"Score","type":"score"}],"sort":[{"field":"score","direction":"desc"}]}],
  "default_views": ["rank"]
}`), 0o644); err != nil {
		t.Fatalf("write box: %v", err)
	}

	registry := NewRegistry()
	registry.RegisterProvider(NewCatalogProvider([]string{root}, nil))

	adapters := registry.List()
	if len(adapters) != 1 || adapters[0].ID != "llm" {
		t.Fatalf("adapters = %#v, want llm", adapters)
	}
	raw, err := registry.Query("llm", "dataset", json.RawMessage(`{"name":"models"}`))
	if err != nil {
		t.Fatalf("Query dataset: %v", err)
	}
	result := raw.(DatasetResult)
	if len(result.Records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(result.Records))
	}
	if result.Records[0].RecordID != "m2" {
		t.Fatalf("first record = %q, want score-sorted m2", result.Records[0].RecordID)
	}
}
