package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

type materialDescriptor struct {
	ID              string            `json:"id"`
	Kind            string            `json:"kind"`
	Action          string            `json:"action"`
	Provenance      string            `json:"provenance"`
	Path            string            `json:"path"`
	PlanSHA256      string            `json:"planSHA256"`
	ManifestSHA256  string            `json:"manifestSHA256"`
	RecordingSHA256 string            `json:"recordingSHA256"`
	Connections     []string          `json:"connections"`
	CreatedAt       string            `json:"createdAt"`
	Files           map[string]string `json:"files"`
}

type materialManifest struct {
	SchemaVersion    int               `json:"schemaVersion"`
	Files            map[string]string `json:"files"`
	PlanSHA256       string            `json:"planSHA256"`
	SourcePlanSHA256 string            `json:"sourcePlanSHA256"`
	SavedFiles       map[string]string `json:"savedFiles"`
	RemotePhaseFiles map[string]string `json:"remotePhaseFiles"`
	Provenance       string            `json:"provenance"`
	RecordingFormat  struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	} `json:"recordingFormat"`
	CaptureStatus string `json:"captureStatus"`
}

type materialPlan struct {
	Provider      string `json:"provider"`
	CaseID        string `json:"caseId"`
	DriverProfile string `json:"driverProfile"`
	Scenario      struct {
		Parameters struct {
			ChildDetailPolicy string `json:"childDetailPolicy"`
		} `json:"parameters"`
	} `json:"scenario"`
}

type officialOrigin struct {
	CaseID             string   `json:"caseId"`
	AgentType          string   `json:"agentType"`
	RecordSourceSHA256 string   `json:"recordSourceSHA256"`
	SourceSHA256       string   `json:"sourceSHA256"`
	Connections        []string `json:"connections"`
}

type materialPackage struct {
	Root          string
	Material      materialDescriptor
	Manifest      materialManifest
	Plan          materialPlan
	PlanBytes     []byte
	AssertionPlan []byte
	ManifestRaw   []byte
	Raw           []byte
	Report        []byte
	ReportSource  string
	ReportSHA256  string
	SourcePlan    []byte
	Origin        *officialOrigin
}

type syntheticEnvelope struct {
	Version    int               `json:"version"`
	Provenance string            `json:"provenance"`
	TimeoutMS  int               `json:"timeoutMs"`
	Frames     []json.RawMessage `json:"frames"`
}

func decodeSyntheticEnvelope(raw []byte) (*syntheticEnvelope, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, false, nil //nolint:nilerr // Raw-frame validation rejects malformed non-envelopes later.
	}
	if _, ok := fields["frames"]; !ok {
		return nil, false, nil
	}
	var envelope syntheticEnvelope
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return nil, true, err
	}
	if envelope.Version != 1 || envelope.Provenance != "synthetic" {
		return nil, true, errors.New("synthetic raw envelope has invalid identity")
	}
	if envelope.TimeoutMS < 0 || len(envelope.Frames) == 0 {
		return nil, true, errors.New("synthetic raw envelope has invalid timeout or frames")
	}
	return &envelope, true, nil
}

func projectRawFrames(raw []byte) ([]byte, error) {
	envelope, synthetic, err := decodeSyntheticEnvelope(raw)
	if err != nil {
		return nil, err
	}
	if synthetic {
		var projected bytes.Buffer
		for _, frame := range envelope.Frames {
			if len(frame) == 0 || !json.Valid(frame) {
				return nil, errors.New("synthetic raw envelope contains an invalid frame")
			}
			var line bytes.Buffer
			if err := json.Compact(&line, frame); err != nil {
				return nil, err
			}
			projected.Write(line.Bytes())
			projected.WriteByte('\n')
		}
		return projected.Bytes(), nil
	}
	frames := 0
	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if !json.Valid(line) {
			return nil, errors.New("recorded raw stream contains an invalid frame")
		}
		frames++
	}
	if frames == 0 {
		return nil, errors.New("recorded raw stream is empty")
	}
	return append([]byte(nil), raw...), nil
}

func validateExport(root string, session *preparedSession) (pkg *materialPackage, err error) {
	root, err = canonicalPath(root)
	if err != nil {
		return nil, err
	}
	if pathsOverlap(root, session.Dir) {
		return nil, errors.New("exported material overlaps the prepared session")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() {
		return nil, errors.New("exported material root is not a directory")
	}
	inventory, err := materialInventory(root)
	if err != nil {
		return nil, err
	}
	materialBytes, err := os.ReadFile(filepath.Join(root, "material.json"))
	if err != nil {
		return nil, fmt.Errorf("exported material descriptor: %w", err)
	}
	pkg = &materialPackage{Root: root}
	if err := json.Unmarshal(materialBytes, &pkg.Material); err != nil {
		return nil, fmt.Errorf("exported material descriptor: %w", err)
	}
	if err := validateMaterialDescriptor(pkg.Material, inventory); err != nil {
		return nil, err
	}
	if err := validateMaterialConnections(pkg.Material.Connections, session.ConnectionIDs()); err != nil {
		return nil, err
	}
	read := func(path string) ([]byte, error) {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	if pkg.PlanBytes, err = read("plan.json"); err != nil {
		return nil, err
	}
	if pkg.ManifestRaw, err = read("manifest.json"); err != nil {
		return nil, err
	}
	if pkg.Raw, err = read("raw/raw.jsonl"); err != nil {
		return nil, err
	}
	if digest(pkg.PlanBytes) != pkg.Material.PlanSHA256 ||
		digest(pkg.ManifestRaw) != pkg.Material.ManifestSHA256 ||
		digest(pkg.Raw) != pkg.Material.RecordingSHA256 {
		return nil, errors.New("exported material identity hashes differ")
	}
	if err := json.Unmarshal(pkg.PlanBytes, &pkg.Plan); err != nil {
		return nil, fmt.Errorf("exported plan: %w", err)
	}
	if pkg.Plan.Provider != session.AgentType || pkg.Plan.CaseID == "" || pkg.Plan.DriverProfile == "" {
		return nil, errors.New("exported plan provider/case/profile is incomplete")
	}
	if session.Source.ID != "" && pkg.Plan.CaseID != session.Source.ID {
		return nil, errors.New("exported plan case differs from the prepared source")
	}
	if err := json.Unmarshal(pkg.ManifestRaw, &pkg.Manifest); err != nil {
		return nil, fmt.Errorf("exported manifest: %w", err)
	}
	if err := validateMaterialManifest(root, pkg, inventory); err != nil {
		return nil, err
	}
	exportReport, err := readOptional(read, "report.json")
	if err != nil {
		return nil, err
	}
	if len(exportReport) > 0 && !json.Valid(exportReport) {
		return nil, errors.New("exported report is not valid JSON")
	}
	if pkg.Report, pkg.ReportSource, err = assertionReport(root, exportReport); err != nil {
		return nil, err
	}
	if len(pkg.Report) > 0 {
		pkg.ReportSHA256 = digest(pkg.Report)
	}
	if pkg.SourcePlan, err = readOptional(read, "source-plan.json"); err != nil {
		return nil, err
	}
	if pkg.Manifest.SourcePlanSHA256 != "" {
		if len(pkg.SourcePlan) == 0 {
			return nil, errors.New("exported source plan is missing")
		}
		if digest(pkg.SourcePlan) != pkg.Manifest.SourcePlanSHA256 {
			return nil, errors.New("source plan hash differs from its manifest binding")
		}
	}
	pkg.AssertionPlan = pkg.PlanBytes
	if len(pkg.SourcePlan) > 0 {
		pkg.AssertionPlan = pkg.SourcePlan
	}
	var assertionPlan materialPlan
	if err := json.Unmarshal(pkg.AssertionPlan, &assertionPlan); err != nil {
		return nil, fmt.Errorf("assertion plan: %w", err)
	}
	if assertionPlan.Provider != pkg.Plan.Provider || assertionPlan.CaseID != pkg.Plan.CaseID ||
		assertionPlan.DriverProfile != pkg.Plan.DriverProfile {
		return nil, errors.New("source and derived plan identities differ")
	}
	if err := validateOfficialOrigin(read, session, pkg); err != nil {
		return nil, err
	}
	if err := validatePreparedMaterialPath(session, pkg.Material.Path); err != nil {
		return nil, err
	}
	if session.Source.Kind == "recording" {
		if !sourceDirectoryDigestMatches(session.Source.Path, session.Source.SHA256) {
			return nil, errors.New("prepared source changed after prepare")
		}
	}
	return pkg, nil
}

func materialInventory(root string) (map[string]string, error) {
	inventory := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("exported material contains a symbolic link: %s", rel)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("exported material contains a non-regular file: %s", rel)
		}
		if !safeRelative(rel) {
			return fmt.Errorf("exported material path is unsafe: %s", rel)
		}
		hash, err := digestPath(path)
		if err != nil {
			return err
		}
		inventory[rel] = hash
		return nil
	})
	return inventory, err
}

func validateMaterialDescriptor(material materialDescriptor, inventory map[string]string) error {
	if material.ID == "" || material.Provenance == "" ||
		(material.Kind != "material" && material.Kind != "capture") ||
		material.PlanSHA256 == "" || material.ManifestSHA256 == "" ||
		material.RecordingSHA256 == "" || len(material.Connections) == 0 {
		return errors.New("exported material identity is incomplete")
	}
	if len(material.Files) == 0 {
		return errors.New("exported material has no complete file inventory")
	}
	withoutDescriptor := make(map[string]string, len(inventory))
	for path, hash := range inventory {
		if path != "material.json" {
			withoutDescriptor[path] = hash
		}
	}
	if !reflect.DeepEqual(withoutDescriptor, material.Files) {
		return errors.New("exported material file inventory differs")
	}
	for _, required := range []string{"plan.json", "manifest.json", "raw/raw.jsonl"} {
		if _, ok := material.Files[required]; !ok {
			return fmt.Errorf("exported material is missing %s", required)
		}
	}
	return nil
}

func validateMaterialConnections(connections, sessionConnections []string) error {
	seen := map[string]bool{}
	keys := append([]string(nil), connections...)
	for _, id := range keys {
		if id != "main" && id != "probe" {
			return fmt.Errorf("exported material has unsupported connection %q", id)
		}
		if seen[id] {
			return fmt.Errorf("exported material repeats connection %q", id)
		}
		seen[id] = true
	}
	if !seen["main"] {
		return errors.New("exported material has no main connection")
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, sessionConnections) {
		return errors.New("exported material connections differ from the mock route")
	}
	return nil
}

func validateMaterialManifest(root string, pkg *materialPackage, inventory map[string]string) error {
	manifest := pkg.Manifest
	if manifest.SchemaVersion != 0 && manifest.SchemaVersion != 1 {
		return fmt.Errorf("unsupported material manifest schemaVersion %d", manifest.SchemaVersion)
	}
	if manifest.PlanSHA256 != pkg.Material.PlanSHA256 ||
		manifest.Files["raw.jsonl"] != pkg.Material.RecordingSHA256 {
		return errors.New("material manifest plan/raw binding differs")
	}
	if manifest.RecordingFormat.Name != "" &&
		(manifest.RecordingFormat.Name != "agent-mock-jsonl" || manifest.RecordingFormat.Version != 1) {
		return errors.New("unsupported material recording format")
	}
	if manifest.CaptureStatus != "" && manifest.CaptureStatus != "complete" {
		return errors.New("material capture is incomplete")
	}
	for path, want := range manifest.Files {
		if !safeRelative(path) {
			return fmt.Errorf("material manifest path is unsafe: %s", path)
		}
		if got := inventory[filepath.Join("raw", path)]; got != want {
			return fmt.Errorf("material manifest file hash differs: %s", path)
		}
	}
	if err := validateBoundFiles(root, pkg.Material.Files, manifest.SavedFiles, "saved history"); err != nil {
		return err
	}
	if err := validateBoundFiles(root, pkg.Material.Files, manifest.RemotePhaseFiles, "remote phase"); err != nil {
		return err
	}
	if _, ok := pkgConnection(pkg, "probe"); ok {
		rawPath := "remote-probe/raw/raw.jsonl"
		if _, exists := pkg.Material.Files[rawPath]; !exists {
			return errors.New("probe connection has no remote phase recording")
		}
		if hash, listed := manifest.RemotePhaseFiles[rawPath]; !listed || hash != pkg.Material.Files[rawPath] {
			return errors.New("probe remote phase is not bound by the manifest")
		}
	}
	return nil
}

func pkgConnection(pkg *materialPackage, id string) (string, bool) {
	for _, connection := range pkg.Material.Connections {
		if connection == id {
			return connection, true
		}
	}
	return "", false
}

func validateBoundFiles(root string, inventory, files map[string]string, label string) error {
	for path, want := range files {
		if !safeRelative(path) {
			return fmt.Errorf("%s path is unsafe: %s", label, path)
		}
		if inventory[path] != want {
			return fmt.Errorf("%s file is not in the material inventory: %s", label, path)
		}
		info, err := os.Lstat(filepath.Join(root, path))
		if err != nil {
			return fmt.Errorf("%s file is missing: %s", label, path)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s file is not a regular file: %s", label, path)
		}
		hash, err := digestPath(filepath.Join(root, path))
		if err != nil {
			return err
		}
		if hash != want {
			return fmt.Errorf("%s hash mismatch: %s", label, path)
		}
	}
	return nil
}

func readOptional(read func(string) ([]byte, error), path string) ([]byte, error) {
	data, err := read(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

func assertionReport(root string, fallback []byte) ([]byte, string, error) {
	const path = "official-replay/verification.json"
	raw, err := os.ReadFile(filepath.Join(root, path))
	if errors.Is(err, os.ErrNotExist) {
		return append([]byte(nil), fallback...), "report.json", nil
	}
	if err != nil {
		return nil, "", err
	}
	var verification struct {
		Report json.RawMessage `json:"report"`
	}
	if err := json.Unmarshal(raw, &verification); err != nil {
		return nil, "", fmt.Errorf("official verification: %w", err)
	}
	report := bytes.TrimSpace(verification.Report)
	if len(report) == 0 || bytes.Equal(report, []byte("null")) {
		return append([]byte(nil), fallback...), "report.json", nil
	}
	if !json.Valid(report) {
		return nil, "", errors.New("official verification report is not valid JSON")
	}
	return append([]byte(nil), report...), path + "#/report", nil
}

func validateOfficialOrigin(read func(string) ([]byte, error), session *preparedSession, pkg *materialPackage) error {
	originBytes, err := readOptional(read, "official-origin.json")
	if err != nil {
		return err
	}
	if len(originBytes) == 0 {
		return nil
	}
	var origin officialOrigin
	if err := json.Unmarshal(originBytes, &origin); err != nil {
		return fmt.Errorf("official origin: %w", err)
	}
	pkg.Origin = &origin
	if origin.CaseID != pkg.Plan.CaseID || origin.AgentType != pkg.Plan.Provider {
		return errors.New("official origin disagrees with the plan")
	}
	if origin.RecordSourceSHA256 == "" || origin.SourceSHA256 == "" ||
		origin.RecordSourceSHA256 != origin.SourceSHA256 {
		return errors.New("official origin source identity is incomplete")
	}
	sourceHash := digest(pkg.SourcePlan)
	if len(pkg.SourcePlan) > 0 && origin.SourceSHA256 != sourceHash {
		return errors.New("official origin source plan hash differs")
	}
	if !reflect.DeepEqual(sortedStrings(origin.Connections), sortedStrings(pkg.Material.Connections)) {
		return errors.New("official origin connections differ from the material")
	}
	if session.Source.Kind == "case" && pkg.Material.Provenance != "builtin" {
		return errors.New("builtin case export is not builtin provenance")
	}
	return nil
}

func validatePreparedMaterialPath(session *preparedSession, path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("exported material source path must be absolute")
	}
	materialPath, err := canonicalPath(path)
	if err != nil {
		return fmt.Errorf("exported material source: %w", err)
	}
	sessionPath, err := canonicalPath(session.Dir)
	if err != nil {
		return err
	}
	if !pathWithin(materialPath, sessionPath) {
		return errors.New("exported material source is outside the prepared session")
	}
	if info, err := os.Stat(materialPath); err != nil || !info.IsDir() {
		return errors.New("exported material source is unavailable")
	}
	return nil
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

type sdkAssertionInputs struct {
	SchemaVersion int    `json:"schemaVersion"`
	Provider      string `json:"provider"`
	CaseID        string `json:"caseId"`
	DriverProfile string `json:"driverProfile"`
	Policy        string `json:"policy"`
	Material      struct {
		ID              string `json:"id"`
		Provenance      string `json:"provenance"`
		PlanSHA256      string `json:"planSHA256"`
		ManifestSHA256  string `json:"manifestSHA256"`
		RecordingSHA256 string `json:"recordingSHA256"`
	} `json:"material"`
	Source struct {
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		SHA256 string `json:"sha256"`
	} `json:"source"`
	Connections []struct {
		ID        string `json:"id"`
		RawFile   string `json:"rawFile"`
		RawSHA256 string `json:"rawSHA256"`
	} `json:"connections"`
	SavedHistory []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"savedHistory"`
}

type runtimePaths struct {
	Workspace   string   `json:"workspace"`
	ProtocolCwd string   `json:"protocolCwd"`
	Config      string   `json:"config"`
	Env         []string `json:"env"`
}

func projectAssertions(root string, pkg *materialPackage, session *preparedSession, policy string) error {
	if err := os.Mkdir(root, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"plan.json", "case.json"} {
		if err := os.WriteFile(filepath.Join(root, name), pkg.AssertionPlan, 0o600); err != nil {
			return err
		}
	}
	if len(pkg.Report) > 0 {
		if err := os.WriteFile(filepath.Join(root, "report.json"), pkg.Report, 0o600); err != nil {
			return err
		}
	}
	type rawEvidence struct {
		Connection        string `json:"connection"`
		SourceFile        string `json:"sourceFile"`
		SourceSHA256      string `json:"sourceSHA256"`
		ProjectedFile     string `json:"projectedFile"`
		ProjectedSHA256   string `json:"projectedSHA256"`
		ProjectedEncoding string `json:"projectedEncoding"`
	}
	projectionEvidence := make([]rawEvidence, 0, len(pkg.Material.Connections))
	connections := make([]struct {
		ID        string `json:"id"`
		RawFile   string `json:"rawFile"`
		RawSHA256 string `json:"rawSHA256"`
	}, 0, len(pkg.Material.Connections))
	for _, id := range pkg.Material.Connections {
		relative := filepath.Join("raw", "raw.jsonl")
		if id == "probe" {
			relative = filepath.Join("remote-probe", "raw", "raw.jsonl")
		}
		sourceData, err := os.ReadFile(filepath.Join(pkg.Root, relative))
		if err != nil {
			return err
		}
		if digest(sourceData) != pkg.Material.Files[relative] {
			return fmt.Errorf("material raw identity changed: %s", relative)
		}
		sourceFile := filepath.Join("source", relative)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, sourceFile)), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, sourceFile), sourceData, 0o600); err != nil {
			return err
		}
		projected, err := projectRawFrames(sourceData)
		if err != nil {
			return fmt.Errorf("project %s: %w", relative, err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, relative)), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, relative), projected, 0o600); err != nil {
			return err
		}
		connections = append(connections, struct {
			ID        string `json:"id"`
			RawFile   string `json:"rawFile"`
			RawSHA256 string `json:"rawSHA256"`
		}{id, relative, pkg.Material.Files[relative]})
		projectionEvidence = append(projectionEvidence, rawEvidence{
			Connection:        id,
			SourceFile:        sourceFile,
			SourceSHA256:      digest(sourceData),
			ProjectedFile:     relative,
			ProjectedSHA256:   digest(projected),
			ProjectedEncoding: "agent-mock-jsonl",
		})
	}
	sort.Slice(connections, func(i, j int) bool { return connections[i].ID < connections[j].ID })
	sort.Slice(projectionEvidence, func(i, j int) bool {
		return projectionEvidence[i].Connection < projectionEvidence[j].Connection
	})
	inputs := sdkAssertionInputs{
		SchemaVersion: 1,
		Provider:      pkg.Plan.Provider,
		CaseID:        pkg.Plan.CaseID,
		DriverProfile: pkg.Plan.DriverProfile,
		Policy:        policy,
		Connections:   connections,
	}
	inputs.Material.ID = pkg.Material.ID
	inputs.Material.Provenance = pkg.Material.Provenance
	inputs.Material.PlanSHA256 = pkg.Material.PlanSHA256
	inputs.Material.ManifestSHA256 = pkg.Material.ManifestSHA256
	inputs.Material.RecordingSHA256 = pkg.Material.RecordingSHA256
	inputs.Source.Kind = session.Source.Kind
	inputs.Source.ID = session.Source.ID
	inputs.Source.SHA256 = session.Source.SHA256
	for path, hash := range pkg.Manifest.SavedFiles {
		inputs.SavedHistory = append(inputs.SavedHistory, struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		}{path, hash})
	}
	sort.Slice(inputs.SavedHistory, func(i, j int) bool { return inputs.SavedHistory[i].Path < inputs.SavedHistory[j].Path })
	config := session.Mock.Env["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = session.Mock.Env["CODEX_HOME"]
	}
	runtime := runtimePaths{
		Workspace:   session.Mock.Workspace,
		ProtocolCwd: session.Mock.ProtocolCwd,
		Config:      config,
		Env:         session.HostEnv(),
	}
	if err := writeJSON(filepath.Join(root, "runtime.json"), runtime); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(root, "assertion-inputs.json"), inputs); err != nil {
		return err
	}
	evidence := map[string]any{
		"schemaVersion": 1,
		"material":      pkg.Material,
		"sourcePlan":    pkg.Origin,
		"rawProjection": projectionEvidence,
		"runtime":       runtime,
		"policy":        policy,
	}
	if len(pkg.Report) > 0 {
		reportFile := strings.Split(pkg.ReportSource, "#")[0]
		evidence["reportProjection"] = map[string]string{
			"source":       pkg.ReportSource,
			"sourceSHA256": pkg.Material.Files[reportFile],
			"sha256":       pkg.ReportSHA256,
			"sdkFile":      "report.json",
		}
	}
	if len(pkg.SourcePlan) > 0 {
		evidence["sourcePlanSHA256"] = digest(pkg.SourcePlan)
	}
	if err := writeJSON(filepath.Join(root, "material-evidence.json"), evidence); err != nil {
		return err
	}
	if got, err := os.ReadFile(filepath.Join(root, "plan.json")); err != nil {
		return err
	} else if !bytes.Equal(got, pkg.AssertionPlan) {
		return errors.New("SDK assertion projection changed source plan bytes")
	}
	for _, item := range projectionEvidence {
		source, err := os.ReadFile(filepath.Join(root, item.SourceFile))
		if err != nil {
			return err
		}
		if digest(source) != item.SourceSHA256 {
			return fmt.Errorf("SDK assertion projection changed source bytes: %s", item.SourceFile)
		}
		expected, err := projectRawFrames(source)
		if err != nil {
			return err
		}
		target, err := os.ReadFile(filepath.Join(root, item.ProjectedFile))
		if err != nil {
			return err
		}
		if digest(target) != item.ProjectedSHA256 || !bytes.Equal(target, expected) {
			return fmt.Errorf("SDK assertion projection changed projected bytes: %s", item.ProjectedFile)
		}
	}
	return nil
}

func planPolicy(pkg *materialPackage) string {
	policy := pkg.Plan.Scenario.Parameters.ChildDetailPolicy
	if policy == "preserve" || policy == "suppress" {
		return policy
	}
	return ""
}

func requestCase(session *preparedSession, pkg *materialPackage) string {
	if pkg.Plan.CaseID != "" {
		return pkg.Plan.CaseID
	}
	return strings.TrimSpace(session.Source.ID)
}
