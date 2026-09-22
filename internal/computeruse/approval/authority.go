package approval

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"time"
)

func readAuthority(path string) (approvalFile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return approvalFile{}, nil
	}
	if err != nil {
		return approvalFile{}, fmt.Errorf("read approvals: %w", err)
	}
	var file approvalFile
	if err := json.Unmarshal(data, &file); err != nil {
		return approvalFile{}, fmt.Errorf("decode approvals: %w", err)
	}
	if file.Version != 1 && file.Version != 2 {
		return approvalFile{}, fmt.Errorf("unsupported approvals version %d", file.Version)
	}
	if file.Version == 2 && file.Epoch == "" {
		return approvalFile{}, errors.New("approval authority epoch missing")
	}
	normalized := make(map[string]approvalRecord, len(file.Approvals))
	for key, record := range file.Approvals {
		id := normalizeBundleID(key)
		if id == "" {
			return approvalFile{}, ErrBundleIDRequired
		}
		if _, exists := normalized[id]; exists {
			return approvalFile{}, errors.New("duplicate normalized approval")
		}
		normalized[id] = record
	}
	file.Approvals = normalized
	for key, revision := range file.Revocations {
		if key == "" || key != normalizeBundleID(key) || revision == 0 || revision > file.Revision {
			return approvalFile{}, errors.New("invalid approval revocation record")
		}
	}
	return file, nil
}

func (f *approvalFile) advance() error {
	if f.Revision == math.MaxUint64 {
		return errors.New("approval revision overflow")
	}
	if f.Epoch == "" {
		var epoch [16]byte
		if _, err := rand.Read(epoch[:]); err != nil {
			return fmt.Errorf("create approval epoch: %w", err)
		}
		f.Epoch = hex.EncodeToString(epoch[:])
	}
	f.Version = 2
	f.Revision++
	f.UpdatedAt = time.Now().UTC()
	if f.Approvals == nil {
		f.Approvals = make(map[string]approvalRecord)
	}
	if f.Revocations == nil {
		f.Revocations = make(map[string]uint64)
	}
	return nil
}

func (f approvalFile) clone() approvalFile {
	f.Approvals = maps.Clone(f.Approvals)
	f.Revocations = maps.Clone(f.Revocations)
	return f
}

func encodeAuthority(file approvalFile) ([]byte, error) {
	data, err := json.MarshalIndent(file, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("encode approvals: %w", err)
	}
	return append(data, '\n'), nil
}
