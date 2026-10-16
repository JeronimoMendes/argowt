package workflows

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// maxManifestBytes bounds local manifests; Kubernetes rejects objects over
// about 1.5 MiB anyway.
const maxManifestBytes = 4 << 20

var documentSeparator = regexp.MustCompile(`(?m)^---[ \t]*(#.*)?$`)

// LoadWorkflowManifest reads a local Workflow manifest. The file must hold
// exactly one document, and that document must be an argoproj.io/v1alpha1
// Workflow: argowt never creates other kinds from a file.
func LoadWorkflowManifest(path string) (*wfv1.Workflow, error) {
	path = ExpandPath(path)
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("manifest path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open manifest: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("manifest %s is larger than %d MiB", path, maxManifestBytes>>20)
	}
	return ParseWorkflowManifest(data)
}

// ParseWorkflowManifest decodes one Workflow document.
func ParseWorkflowManifest(data []byte) (*wfv1.Workflow, error) {
	var documents []string
	for _, document := range documentSeparator.Split(string(data), -1) {
		if hasContent(document) {
			documents = append(documents, document)
		}
	}
	switch len(documents) {
	case 0:
		return nil, fmt.Errorf("manifest is empty")
	case 1:
	default:
		return nil, fmt.Errorf("manifest has %d documents; argowt submits one Workflow per file", len(documents))
	}

	var typeMeta metav1.TypeMeta
	if err := yaml.Unmarshal([]byte(documents[0]), &typeMeta); err != nil {
		return nil, fmt.Errorf("manifest is not valid YAML: %w", err)
	}
	if typeMeta.Kind != "Workflow" || typeMeta.APIVersion != wfv1.SchemeGroupVersion.String() {
		found := strings.TrimSpace(typeMeta.APIVersion + " " + typeMeta.Kind)
		if found == "" {
			found = "no apiVersion or kind"
		}
		return nil, fmt.Errorf("manifest is %s; only %s Workflow manifests can be submitted", found, wfv1.SchemeGroupVersion.String())
	}

	var workflow wfv1.Workflow
	if err := yaml.Unmarshal([]byte(documents[0]), &workflow); err != nil {
		return nil, fmt.Errorf("decode Workflow manifest: %w", err)
	}
	if workflow.Name == "" && workflow.GenerateName == "" {
		return nil, fmt.Errorf("manifest needs metadata.name or metadata.generateName")
	}

	// A manifest exported from a cluster carries server-owned fields that a
	// create would reject or misread.
	workflow.UID = ""
	workflow.ResourceVersion = ""
	workflow.Generation = 0
	workflow.CreationTimestamp = metav1.Time{}
	workflow.ManagedFields = nil
	workflow.Status = wfv1.WorkflowStatus{}
	return &workflow, nil
}

// hasContent reports whether a document contains a nonblank line that is not a YAML comment.
func hasContent(document string) bool {
	for _, line := range strings.Split(document, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return true
		}
	}
	return false
}

// ExpandPath resolves a leading ~ to the home directory.
func ExpandPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}
