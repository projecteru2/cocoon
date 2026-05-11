package core

import (
	"context"
	"testing"

	"github.com/cocoonstack/cocoon/gc"
	imagebackend "github.com/cocoonstack/cocoon/images"
	"github.com/cocoonstack/cocoon/progress"
	"github.com/cocoonstack/cocoon/types"
)

// fakeImageBackend records Pull invocations so the test can assert how
// EnsureImage decided to call it.
type fakeImageBackend struct {
	typ string

	inspectByRef    map[string]*types.Image // ref → image (nil if not local)
	pullRefs        []string                // ordered Pull arg history
	pullForce       []bool
	pullErr         error
	postPullInspect map[string]*types.Image // ref → image (second Inspect after Pull)
}

func (f *fakeImageBackend) Type() string { return f.typ }

func (f *fakeImageBackend) Pull(_ context.Context, ref string, force bool, _ progress.Tracker) error {
	f.pullRefs = append(f.pullRefs, ref)
	f.pullForce = append(f.pullForce, force)
	return f.pullErr
}

func (f *fakeImageBackend) Inspect(_ context.Context, ref string) (*types.Image, error) {
	// First call uses inspectByRef; once Pull has been called, switch to
	// postPullInspect so the test can simulate "blob now landed locally".
	if len(f.pullRefs) > 0 && f.postPullInspect != nil {
		return f.postPullInspect[ref], nil
	}
	return f.inspectByRef[ref], nil
}

func (f *fakeImageBackend) Import(context.Context, string, progress.Tracker, ...string) error {
	return nil
}

func (f *fakeImageBackend) List(context.Context) ([]*types.Image, error) { return nil, nil }
func (f *fakeImageBackend) Delete(context.Context, []string) ([]string, error) {
	return nil, nil
}
func (f *fakeImageBackend) RegisterGC(*gc.Orchestrator) {}
func (f *fakeImageBackend) Config(context.Context, []*types.VMConfig) ([][]*types.StorageConfig, []*types.BootConfig, error) {
	return nil, nil, nil
}

// TestEnsureImage_ForceWhenDigestPinned is the regression guard for issue
// 37: when vmCfg.ImageDigest is set and the digest isn't local, EnsureImage
// must pass force=true to Pull so the cloudimg URL-level short-circuit
// (which keys on URL, not digest) re-fetches the blob instead of accepting
// whatever stale content is cached under the same URL.
func TestEnsureImage_ForceWhenDigestPinned(t *testing.T) {
	const (
		url    = "https://epoch.example/dl/simular/win11"
		digest = "sha256:adafd938488daa114be898848eb24b9b0afffc21ac18f8b11f3f0057644b11e1"
	)

	tests := []struct {
		name        string
		vmCfg       *types.VMConfig
		wantPullRef string
		wantForce   bool
	}{
		{
			name: "digest pinned → force=true",
			vmCfg: &types.VMConfig{
				Config: types.Config{
					Image:       url,
					ImageDigest: digest,
					ImageType:   types.ImageTypeCloudImg,
				},
			},
			wantPullRef: url,
			wantForce:   true,
		},
		{
			name: "no digest → force=false",
			vmCfg: &types.VMConfig{
				Config: types.Config{
					Image:     url,
					ImageType: types.ImageTypeCloudImg,
				},
			},
			wantPullRef: url,
			wantForce:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeImageBackend{typ: types.ImageTypeCloudImg}
			EnsureImage(t.Context(), []imagebackend.Images{f}, tt.vmCfg)
			if len(f.pullRefs) != 1 {
				t.Fatalf("Pull invocations = %d, want 1", len(f.pullRefs))
			}
			if f.pullRefs[0] != tt.wantPullRef {
				t.Errorf("Pull ref = %q, want %q", f.pullRefs[0], tt.wantPullRef)
			}
			if f.pullForce[0] != tt.wantForce {
				t.Errorf("Pull force = %v, want %v", f.pullForce[0], tt.wantForce)
			}
		})
	}
}

// TestEnsureImage_SkipsPullWhenDigestLocal: the happy path. If Inspect by
// digest hits, EnsureImage returns without calling Pull at all — the
// expensive force-pull only fires when we've proven the digest isn't local.
func TestEnsureImage_SkipsPullWhenDigestLocal(t *testing.T) {
	const digest = "sha256:adafd938488daa114be898848eb24b9b0afffc21ac18f8b11f3f0057644b11e1"
	f := &fakeImageBackend{
		typ: types.ImageTypeCloudImg,
		inspectByRef: map[string]*types.Image{
			digest: {ID: digest, Name: "win11", Type: types.ImageTypeCloudImg},
		},
	}
	EnsureImage(t.Context(), []imagebackend.Images{f}, &types.VMConfig{
		Config: types.Config{
			Image:       "https://epoch.example/dl/simular/win11",
			ImageDigest: digest,
			ImageType:   types.ImageTypeCloudImg,
		},
	})
	if len(f.pullRefs) != 0 {
		t.Errorf("Pull was called %d time(s) with %v, want 0 (digest is local)", len(f.pullRefs), f.pullRefs)
	}
}
