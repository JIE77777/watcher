package box

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type CatalogProvider struct {
	roots          []string
	sourceAdapters map[string]Adapter
}

func NewCatalogProvider(roots []string, sourceAdapters map[string]Adapter) *CatalogProvider {
	cleanRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root != "" {
			cleanRoots = append(cleanRoots, filepath.Clean(root))
		}
	}
	return &CatalogProvider{
		roots:          cleanRoots,
		sourceAdapters: sourceAdapters,
	}
}

func (p *CatalogProvider) List() []AdapterInfo {
	defs := p.definitions()
	out := make([]AdapterInfo, 0, len(defs))
	for _, def := range defs {
		out = append(out, AdapterInfo{
			ID:          def.ID,
			Title:       firstNonBlank(def.Title, def.ID),
			Description: def.Description,
			QueryTypes:  catalogQueryTypes(),
			Kind:        "box",
		})
	}
	return out
}

func (p *CatalogProvider) Get(id string) (Adapter, bool) {
	if _, ok := p.definition(id); !ok {
		return nil, false
	}
	return &CatalogAdapter{id: id, provider: p}, true
}

func (p *CatalogProvider) definition(id string) (BoxDefinition, bool) {
	for _, def := range p.definitions() {
		if def.ID == id {
			return def, true
		}
	}
	return BoxDefinition{}, false
}

func (p *CatalogProvider) definitions() []BoxDefinition {
	defs := []BoxDefinition{}
	for _, root := range p.roots {
		matches, _ := filepath.Glob(filepath.Join(root, "*.box.json"))
		sort.Strings(matches)
		for _, path := range matches {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var def BoxDefinition
			if err := json.Unmarshal(data, &def); err != nil {
				continue
			}
			def.ID = strings.TrimSpace(def.ID)
			if def.ID == "" {
				continue
			}
			def.path = path
			def.baseDir = filepath.Dir(path)
			defs = append(defs, def)
		}
	}
	sort.SliceStable(defs, func(i, j int) bool {
		if defs[i].Order != defs[j].Order {
			return defs[i].Order < defs[j].Order
		}
		if defs[i].Title != defs[j].Title {
			return defs[i].Title < defs[j].Title
		}
		return defs[i].ID < defs[j].ID
	})
	return defs
}

type CatalogAdapter struct {
	id       string
	provider *CatalogProvider
}

func (a *CatalogAdapter) ID() string { return a.id }

func (a *CatalogAdapter) QueryTypes() []string { return catalogQueryTypes() }

func catalogQueryTypes() []string {
	return []string{"catalog", "dataset", "datasets", "view", "views", "signals"}
}

func (a *CatalogAdapter) Query(queryType string, params json.RawMessage) (any, error) {
	def, ok := a.provider.definition(a.id)
	if !ok {
		return nil, fmt.Errorf("box %q not found", a.id)
	}
	switch queryType {
	case "catalog":
		return def.publicCatalog(), nil
	case "datasets":
		return def.Datasets, nil
	case "views":
		return def.Views, nil
	case "view":
		var p struct {
			ViewID string `json:"view_id"`
		}
		_ = json.Unmarshal(params, &p)
		view, ok := def.findView(p.ViewID)
		if !ok {
			return nil, fmt.Errorf("view %q not found", p.ViewID)
		}
		return view, nil
	case "dataset":
		return a.queryDataset(def, params)
	case "signals":
		return def.Signals, nil
	default:
		return nil, fmt.Errorf("unknown query type: %s", queryType)
	}
}

func (a *CatalogAdapter) queryDataset(def BoxDefinition, params json.RawMessage) (DatasetResult, error) {
	var p struct {
		Name      string         `json:"name"`
		DatasetID string         `json:"dataset_id"`
		ViewID    string         `json:"view_id"`
		Filter    map[string]any `json:"filter"`
		Limit     int            `json:"limit"`
	}
	_ = json.Unmarshal(params, &p)
	dataset, ok := def.findDataset(p.Name, p.DatasetID)
	if !ok && p.ViewID != "" {
		if view, viewOK := def.findView(p.ViewID); viewOK {
			dataset, ok = def.findDataset(view.DatasetID, view.DatasetID)
		}
	}
	if !ok {
		return DatasetResult{}, fmt.Errorf("dataset not found")
	}
	view, _ := def.findView(firstNonBlank(p.ViewID, dataset.ViewID))
	raw, err := a.loadDatasetSource(def, dataset, p)
	if err != nil {
		return DatasetResult{}, err
	}
	records := normalizeDatasetRecords(raw, firstNonBlank(view.GroupBy, dataset.GroupBy, "group"))
	records = filterDatasetRecords(records, p.Filter)
	sortDatasetRecords(records, firstNonEmptySort(view.Sort, dataset.Sort))
	if p.Limit > 0 && len(records) > p.Limit {
		records = records[:p.Limit]
	}
	return DatasetResult{
		DatasetID: firstNonBlank(dataset.ID, dataset.Name),
		Name:      firstNonBlank(dataset.Name, dataset.ID),
		Kind:      firstNonBlank(dataset.Kind, view.Type, "records"),
		ViewID:    firstNonBlank(dataset.ViewID, view.ID),
		Records:   records,
	}, nil
}

func (a *CatalogAdapter) loadDatasetSource(def BoxDefinition, dataset BoxDataset, p struct {
	Name      string         `json:"name"`
	DatasetID string         `json:"dataset_id"`
	ViewID    string         `json:"view_id"`
	Filter    map[string]any `json:"filter"`
	Limit     int            `json:"limit"`
}) (any, error) {
	source, ok := def.findSource(dataset.SourceID)
	if !ok {
		return nil, fmt.Errorf("source %q not found", dataset.SourceID)
	}
	switch source.Kind {
	case "fixture_json", "file_json", "":
		return loadFixtureDataset(def, source, dataset)
	case "adapter_query":
		adapterID := firstNonBlank(source.AdapterID, source.ID)
		adapter, ok := a.provider.sourceAdapters[adapterID]
		if !ok {
			return nil, fmt.Errorf("source adapter %q not registered", adapterID)
		}
		queryType := firstNonBlank(dataset.QueryType, source.QueryType, dataset.SourceDataset, dataset.ID)
		queryParams := mergeParams(source.Params, dataset.Params)
		if dataset.SourceDataset != "" {
			queryParams["name"] = dataset.SourceDataset
			queryParams["dataset_id"] = dataset.SourceDataset
		}
		if p.Limit > 0 {
			queryParams["limit"] = p.Limit
		}
		if len(p.Filter) > 0 {
			queryParams["filter"] = p.Filter
		}
		rawParams, _ := json.Marshal(queryParams)
		return adapter.Query(queryType, rawParams)
	default:
		return nil, fmt.Errorf("unsupported source kind %q", source.Kind)
	}
}

func loadFixtureDataset(def BoxDefinition, source BoxSource, dataset BoxDataset) (any, error) {
	path := strings.TrimSpace(source.Path)
	if path == "" {
		return nil, fmt.Errorf("fixture source %q missing path", source.ID)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(def.baseDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	name := firstNonBlank(dataset.SourceDataset, dataset.Name, dataset.ID)
	if root, ok := raw.(map[string]any); ok {
		if datasets, ok := root["datasets"].(map[string]any); ok {
			if selected, ok := datasets[name]; ok {
				return selected, nil
			}
		}
	}
	return raw, nil
}

type BoxDefinition struct {
	SchemaVersion string       `json:"schema_version,omitempty"`
	ID            string       `json:"id"`
	Title         string       `json:"title"`
	Description   string       `json:"description,omitempty"`
	Order         int          `json:"order,omitempty"`
	Sources       []BoxSource  `json:"sources"`
	Datasets      []BoxDataset `json:"datasets"`
	Views         []BoxView    `json:"views"`
	DefaultViews  []string     `json:"default_views,omitempty"`
	Signals       []BoxSignal  `json:"signals,omitempty"`
	path          string       `json:"-"`
	baseDir       string       `json:"-"`
}

type BoxSource struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Path      string         `json:"path,omitempty"`
	AdapterID string         `json:"adapter_id,omitempty"`
	QueryType string         `json:"query_type,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
}

type BoxDataset struct {
	ID            string         `json:"id"`
	Name          string         `json:"name,omitempty"`
	Title         string         `json:"title,omitempty"`
	Kind          string         `json:"kind,omitempty"`
	SourceID      string         `json:"source_id"`
	SourceDataset string         `json:"source_dataset,omitempty"`
	QueryType     string         `json:"query_type,omitempty"`
	Params        map[string]any `json:"params,omitempty"`
	ViewID        string         `json:"view_id,omitempty"`
	GroupBy       string         `json:"group_by,omitempty"`
	PrimaryKeys   []string       `json:"primary_keys,omitempty"`
	Sort          []BoxSort      `json:"sort,omitempty"`
}

type BoxView struct {
	ID        string          `json:"view_id"`
	Type      string          `json:"type"`
	Title     string          `json:"title"`
	DatasetID string          `json:"dataset_id,omitempty"`
	GroupBy   string          `json:"group_by,omitempty"`
	Columns   []BoxViewColumn `json:"columns"`
	Sort      []BoxSort       `json:"sort,omitempty"`
}

type BoxViewColumn struct {
	Field string `json:"field"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"`
}

type BoxSort struct {
	Field     string `json:"field"`
	Direction string `json:"direction,omitempty"`
}

type BoxSignal struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Dataset string `json:"dataset,omitempty"`
	Field   string `json:"field,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type DatasetResult struct {
	DatasetID string          `json:"dataset_id"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	ViewID    string          `json:"view_id,omitempty"`
	Records   []DatasetRecord `json:"records"`
}

type DatasetRecord struct {
	RecordID string         `json:"record_id"`
	Title    string         `json:"title"`
	Subtitle string         `json:"subtitle,omitempty"`
	Data     map[string]any `json:"data"`
}

func (def BoxDefinition) publicCatalog() map[string]any {
	return map[string]any{
		"schema_version": firstNonBlank(def.SchemaVersion, "box.v1"),
		"id":             def.ID,
		"title":          firstNonBlank(def.Title, def.ID),
		"description":    def.Description,
		"datasets":       def.Datasets,
		"views":          def.Views,
		"default_views":  def.DefaultViews,
		"signals":        def.Signals,
	}
}

func (def BoxDefinition) findSource(id string) (BoxSource, bool) {
	for _, source := range def.Sources {
		if source.ID == id {
			return source, true
		}
	}
	return BoxSource{}, false
}

func (def BoxDefinition) findDataset(name, id string) (BoxDataset, bool) {
	for _, dataset := range def.Datasets {
		if id != "" && dataset.ID == id {
			return dataset, true
		}
		if name != "" && (dataset.Name == name || dataset.ID == name) {
			return dataset, true
		}
	}
	if name == "" && id == "" && len(def.Datasets) > 0 {
		return def.Datasets[0], true
	}
	return BoxDataset{}, false
}

func (def BoxDefinition) findView(id string) (BoxView, bool) {
	for _, view := range def.Views {
		if view.ID == id {
			return view, true
		}
	}
	if id == "" && len(def.Views) > 0 {
		return def.Views[0], true
	}
	return BoxView{}, false
}

func normalizeDatasetRecords(raw any, groupField string) []DatasetRecord {
	raw = jsonCompatible(raw)
	records := []DatasetRecord{}
	switch value := raw.(type) {
	case map[string]any:
		if direct, ok := value["records"]; ok {
			return normalizeRecordList(direct, "", groupField)
		}
		if entries, ok := value["entries"]; ok {
			return normalizeRecordList(entries, "", groupField)
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			groupRaw := value[key]
			if groupMap, ok := groupRaw.(map[string]any); ok {
				if entries, ok := groupMap["records"]; ok {
					records = append(records, normalizeRecordList(entries, key, groupField)...)
					continue
				}
				if entries, ok := groupMap["entries"]; ok {
					records = append(records, normalizeRecordList(entries, key, groupField)...)
					continue
				}
			}
			records = append(records, normalizeOneRecord(groupRaw, key, "", groupField))
		}
	case []any:
		records = append(records, normalizeRecordList(value, "", groupField)...)
	default:
		records = append(records, normalizeOneRecord(value, "record", "", groupField))
	}
	return records
}

func jsonCompatible(raw any) any {
	switch raw.(type) {
	case nil, map[string]any, []any, string, float64, bool:
		return raw
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return raw
		}
		var out any
		if err := json.Unmarshal(data, &out); err != nil {
			return raw
		}
		return out
	}
}

func normalizeRecordList(raw any, group string, groupField string) []DatasetRecord {
	list, ok := raw.([]any)
	if !ok {
		return []DatasetRecord{normalizeOneRecord(raw, "record", group, groupField)}
	}
	records := make([]DatasetRecord, 0, len(list))
	for i, item := range list {
		records = append(records, normalizeOneRecord(item, fmt.Sprintf("%03d", i+1), group, groupField))
	}
	return records
}

func normalizeOneRecord(raw any, fallbackID string, group string, groupField string) DatasetRecord {
	data := anyToMap(raw)
	if nested, ok := data["data"].(map[string]any); ok {
		for key, value := range nested {
			data[key] = value
		}
	}
	if group != "" && groupField != "" {
		if _, exists := data[groupField]; !exists {
			data[groupField] = group
		}
	}
	recordID := firstNonBlank(stringValue(data["record_id"]), stringValue(data["id"]), stringValue(data["model_id"]), stringValue(data["team"]), fallbackID)
	title := firstNonBlank(stringValue(data["title"]), stringValue(data["model"]), stringValue(data["name"]), stringValue(data["team"]), recordID)
	subtitle := firstNonBlank(stringValue(data["subtitle"]), stringValue(data["provider"]), stringValue(data["unit"]), stringValue(data[groupField]))
	delete(data, "data")
	return DatasetRecord{
		RecordID: recordID,
		Title:    title,
		Subtitle: subtitle,
		Data:     data,
	}
}

func anyToMap(raw any) map[string]any {
	if raw == nil {
		return map[string]any{}
	}
	if value, ok := raw.(map[string]any); ok {
		out := make(map[string]any, len(value))
		for key, item := range value {
			out[key] = item
		}
		return out
	}
	data, _ := json.Marshal(raw)
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	if len(out) == 0 {
		out["value"] = raw
	}
	return out
}

func filterDatasetRecords(records []DatasetRecord, filter map[string]any) []DatasetRecord {
	if len(filter) == 0 {
		return records
	}
	out := make([]DatasetRecord, 0, len(records))
	for _, record := range records {
		match := true
		for key, want := range filter {
			if fmt.Sprint(record.Data[key]) != fmt.Sprint(want) {
				match = false
				break
			}
		}
		if match {
			out = append(out, record)
		}
	}
	return out
}

func sortDatasetRecords(records []DatasetRecord, sorts []BoxSort) {
	if len(sorts) == 0 {
		return
	}
	sortSpec := sorts[0]
	if sortSpec.Field == "" {
		return
	}
	desc := strings.EqualFold(sortSpec.Direction, "desc")
	sort.SliceStable(records, func(i, j int) bool {
		cmp := compareValues(records[i].Data[sortSpec.Field], records[j].Data[sortSpec.Field])
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
}

func compareValues(a, b any) int {
	if af, ok := numberValue(a); ok {
		if bf, ok := numberValue(b); ok {
			switch {
			case af < bf:
				return -1
			case af > bf:
				return 1
			default:
				return 0
			}
		}
	}
	as := strings.ToLower(stringValue(a))
	bs := strings.ToLower(stringValue(b))
	switch {
	case as < bs:
		return -1
	case as > bs:
		return 1
	default:
		return 0
	}
}

func numberValue(v any) (float64, bool) {
	switch value := v.(type) {
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case float64:
		return value, true
	case json.Number:
		f, err := value.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(value, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func mergeParams(items ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, item := range items {
		for key, value := range item {
			out[key] = value
		}
	}
	return out
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptySort(items ...[]BoxSort) []BoxSort {
	for _, item := range items {
		if len(item) > 0 {
			return item
		}
	}
	return nil
}

func stringValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}
