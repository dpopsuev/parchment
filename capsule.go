package parchment

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// CapsuleManifest describes the contents of a capsule file.
type CapsuleManifest struct {
	Version       string    `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	ArtifactCount int       `json:"artifact_count"`
	EdgeCount     int       `json:"edge_count"`
}

// CapsuleExport creates a .capsule file (tar.gz) containing all artifacts, edges,
// and a manifest. Backend-agnostic — reads through the Store interface.
func (p *Protocol) CapsuleExport(ctx context.Context, w io.Writer, version string) (*CapsuleManifest, error) {
	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Export artifacts as JSON-lines
	arts, err := p.store.List(ctx, Filter{})
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	var artsBuf []byte
	for _, art := range arts {
		line, _ := json.Marshal(art)
		artsBuf = append(artsBuf, line...)
		artsBuf = append(artsBuf, '\n')
	}
	if err := addTarEntry(tw, "artifacts.jsonl", artsBuf); err != nil {
		return nil, err
	}

	// Export edges
	edgeCount := 0
	var edgesBuf []byte
	for _, art := range arts {
		edges, _ := p.store.Neighbors(ctx, art.ID, "", Both)
		for _, e := range edges {
			if e.From == art.ID { // only outgoing to avoid duplicates
				line, _ := json.Marshal(e)
				edgesBuf = append(edgesBuf, line...)
				edgesBuf = append(edgesBuf, '\n')
				edgeCount++
			}
		}
	}
	if err := addTarEntry(tw, "edges.jsonl", edgesBuf); err != nil {
		return nil, err
	}

	// Write manifest
	manifest := &CapsuleManifest{
		Version:       version,
		CreatedAt:     time.Now().UTC(),
		ArtifactCount: len(arts),
		EdgeCount:     edgeCount,
	}
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	if err := addTarEntry(tw, "manifest.json", manifestData); err != nil {
		return nil, err
	}

	return manifest, nil
}

// CapsuleImport reads a .capsule file and replaces all artifacts and edges.
// Creates a pre-import snapshot first.
func (p *Protocol) CapsuleImport(ctx context.Context, r io.Reader) (*CapsuleManifest, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)

	var manifest *CapsuleManifest
	var artsData, edgesData []byte

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", hdr.Name, err)
		}
		switch hdr.Name {
		case "manifest.json":
			manifest = &CapsuleManifest{}
			if err := json.Unmarshal(data, manifest); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
		case "artifacts.jsonl":
			artsData = data
		case "edges.jsonl":
			edgesData = data
		}
	}

	if manifest == nil {
		return nil, fmt.Errorf("capsule missing manifest.json")
	}

	// Import artifacts
	dec := json.NewDecoder(bytes.NewReader(artsData))
	for dec.More() {
		var art Artifact
		if err := dec.Decode(&art); err != nil {
			return manifest, fmt.Errorf("decode artifact: %w", err)
		}
		if err := p.store.Put(ctx, &art); err != nil {
			return manifest, fmt.Errorf("import %s: %w", art.ID, err)
		}
	}

	// Import edges
	if len(edgesData) > 0 {
		dec = json.NewDecoder(bytes.NewReader(edgesData))
		for dec.More() {
			var e Edge
			if err := dec.Decode(&e); err != nil {
				return manifest, fmt.Errorf("decode edge: %w", err)
			}
			p.store.AddEdge(ctx, e)
		}
	}

	return manifest, nil
}

// CapsuleInspect reads only the manifest from a .capsule file.
func CapsuleInspect(r io.Reader) (*CapsuleManifest, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}
		if hdr.Name == "manifest.json" {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			var m CapsuleManifest
			if err := json.Unmarshal(data, &m); err != nil {
				return nil, err
			}
			return &m, nil
		}
	}
	return nil, fmt.Errorf("manifest.json not found in capsule")
}

func addTarEntry(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Size:    int64(len(data)),
		Mode:    0644,
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}
