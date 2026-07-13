package productworkflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type workflowCompletionInputState string

const (
	workflowCompletionInputAbsent    workflowCompletionInputState = "absent"
	workflowCompletionInputFile      workflowCompletionInputState = "file"
	workflowCompletionInputDirectory workflowCompletionInputState = "directory"
)

type workflowCompletionInputObservation struct {
	state  workflowCompletionInputState
	digest string
}

type workflowCompletionExternalInput struct {
	key     string
	value   string
	inspect func() string
}

// workflowCompletionInputRecorder captures only inputs actually consumed by
// completion loading, lint, and coverage. Repeated observations must agree,
// and Verify rereads exactly this path set immediately before phase commit.
type workflowCompletionInputRecorder struct {
	observations map[string]workflowCompletionInputObservation
	external     map[string]workflowCompletionExternalInput
	err          error
}

func newWorkflowCompletionInputRecorder() *workflowCompletionInputRecorder {
	return &workflowCompletionInputRecorder{
		observations: map[string]workflowCompletionInputObservation{},
		external:     map[string]workflowCompletionExternalInput{},
	}
}

func (recorder *workflowCompletionInputRecorder) ReadFile(root *os.Root, relPath string) ([]byte, error) {
	body, err := readEffortArtifactRoot(root, relPath)
	recorder.observeFile(relPath, body, err)

	return body, err
}

func (recorder *workflowCompletionInputRecorder) ObserveExternal(input workflowCompletionExternalInput) {
	if recorder == nil || recorder.err != nil {
		return
	}
	if input.key == "" || input.inspect == nil {
		recorder.fail(errors.New("completion external input requires a key and inspector"))
		return
	}
	if previous, ok := recorder.external[input.key]; ok && previous.value != input.value {
		recorder.fail(fmt.Errorf("completion external input %s changed while loading", input.key))
		return
	}
	recorder.external[input.key] = input
}

func (recorder *workflowCompletionInputRecorder) Verify(root *os.Root) error {
	if recorder == nil {
		return errors.New("completion input recorder is required")
	}
	if recorder.err != nil {
		return recorder.err
	}
	paths := make([]string, 0, len(recorder.observations))
	for relPath := range recorder.observations {
		paths = append(paths, relPath)
	}
	sort.Strings(paths)
	for _, relPath := range paths {
		expected := recorder.observations[relPath]
		current, err := inspectWorkflowCompletionInput(root, relPath)
		if err != nil {
			return err
		}
		if current != expected {
			return fmt.Errorf("completion input %s changed during validation", filepath.ToSlash(relPath))
		}
	}
	externalKeys := make([]string, 0, len(recorder.external))
	for key := range recorder.external {
		externalKeys = append(externalKeys, key)
	}
	sort.Strings(externalKeys)
	for _, key := range externalKeys {
		input := recorder.external[key]
		if current := input.inspect(); current != input.value {
			return fmt.Errorf("completion external input %s changed during validation", key)
		}
	}

	return nil
}

func (recorder *workflowCompletionInputRecorder) observeFile(relPath string, body []byte, err error) {
	cleanPath, ok := cleanEffortRelativePath(relPath)
	if !ok {
		recorder.fail(fmt.Errorf("completion input path %q must be effort-relative", relPath))
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		recorder.record(cleanPath, workflowCompletionInputObservation{state: workflowCompletionInputAbsent})
		return
	}
	if err != nil {
		return
	}
	sum := sha256.Sum256(body)
	recorder.record(cleanPath, workflowCompletionInputObservation{
		state:  workflowCompletionInputFile,
		digest: hex.EncodeToString(sum[:]),
	})
}

func (recorder *workflowCompletionInputRecorder) record(
	relPath string,
	observation workflowCompletionInputObservation,
) {
	if recorder == nil {
		return
	}
	if previous, ok := recorder.observations[relPath]; ok && previous != observation {
		recorder.fail(fmt.Errorf("completion input %s changed while loading", filepath.ToSlash(relPath)))
		return
	}
	recorder.observations[relPath] = observation
}

func (recorder *workflowCompletionInputRecorder) fail(err error) {
	if recorder != nil && recorder.err == nil {
		recorder.err = err
	}
}

func workflowCompletionFileValue(body []byte, err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "missing"
	case err != nil:
		return "error"
	default:
		sum := sha256.Sum256(body)
		return "present:" + hex.EncodeToString(sum[:])
	}
}

func workflowCompletionStringSliceValue(values []string, err error) string {
	if err != nil {
		return "error"
	}
	if values == nil {
		values = []string{}
	}
	body, marshalErr := json.Marshal(values)
	if marshalErr != nil {
		return "error"
	}

	return "values:" + string(body)
}

func inspectWorkflowCompletionInput(root *os.Root, relPath string) (workflowCompletionInputObservation, error) {
	info, err := root.Lstat(relPath)
	if errors.Is(err, os.ErrNotExist) {
		return workflowCompletionInputObservation{state: workflowCompletionInputAbsent}, nil
	}
	if err != nil {
		return workflowCompletionInputObservation{}, fmt.Errorf("inspect completion input %s: %w", filepath.ToSlash(relPath), err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return workflowCompletionInputObservation{}, fmt.Errorf("completion input %s must not be a symlink", filepath.ToSlash(relPath))
	}
	if info.IsDir() {
		return workflowCompletionInputObservation{state: workflowCompletionInputDirectory}, nil
	}
	if !info.Mode().IsRegular() {
		return workflowCompletionInputObservation{}, fmt.Errorf("completion input %s must be a regular file or directory", filepath.ToSlash(relPath))
	}
	body, err := readEffortArtifactRoot(root, relPath)
	if err != nil {
		return workflowCompletionInputObservation{}, err
	}
	sum := sha256.Sum256(body)

	return workflowCompletionInputObservation{state: workflowCompletionInputFile, digest: hex.EncodeToString(sum[:])}, nil
}
