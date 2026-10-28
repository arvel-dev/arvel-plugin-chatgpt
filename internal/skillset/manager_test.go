// SPDX-License-Identifier: Apache-2.0
package skillset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-sdk/protocol"
	"github.com/arvel-dev/arvel-sdk/protocol/skills"
)

func fixture(t *testing.T) (*Manager, string, protocol.SkillRef) {
	t.Helper()
	root := t.TempDir()
	cwd := filepath.Join(root, "workspace")
	private := filepath.Join(root, "private")
	source := filepath.Join(root, "source")
	for _, path := range []string{cwd, private, source} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: review\ndescription: Owned test skill.\n---\nSOURCE_BODY\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := New(private)
	t.Cleanup(m.Close)
	return m, cwd, protocol.SkillRef{Name: "review", Path: source, Invocation: protocol.SkillInvocationAuto}
}

func TestSDKIndependentCleanup_PreservesAnotherSessionCopy(t *testing.T) {
	_, cwd, ref := fixture(t)
	first, err := skills.Materialize(cwd, []string{filepath.FromSlash(projectDir)}, []protocol.SkillRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	second, err := skills.Materialize(cwd, []string{filepath.FromSlash(projectDir)}, []protocol.SkillRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	first()
	if _, err := os.Stat(filepath.Join(cwd, filepath.FromSlash(projectDir), "arvel-"+ref.Name, "SKILL.md")); err != nil {
		t.Fatal("SDK cleanup removed another session copy")
	}
}

func TestManager_IdenticalAssignmentsShareLifetimeAndPreserveSource(t *testing.T) {
	m, cwd, ref := fixture(t)
	original, _ := os.ReadFile(filepath.Join(ref.Path, "SKILL.md"))
	first, releaseFirst, err := m.Acquire(t.Context(), cwd, []protocol.SkillRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	second, releaseSecond, err := m.Acquire(t.Context(), cwd, []protocol.SkillRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Path != second[0].Path {
		t.Fatal("copy was not shared")
	}
	releaseFirst()
	releaseFirst()
	if _, err := os.Stat(filepath.Join(second[0].Path, "SKILL.md")); err != nil {
		t.Fatal("early close deleted live copy")
	}
	releaseSecond()
	if _, err := os.Stat(second[0].Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("last close retained copy")
	}
	current, _ := os.ReadFile(filepath.Join(ref.Path, "SKILL.md"))
	if string(current) != string(original) {
		t.Fatal("external original changed")
	}
}

func TestManager_ConflictsChangedSourcesAndOtherManagersAreBusy(t *testing.T) {
	m, cwd, ref := fixture(t)
	catalog, release, err := m.Acquire(t.Context(), cwd, []protocol.SkillRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, _, err := m.Acquire(t.Context(), cwd, nil); !errors.Is(err, ErrBusy) {
		t.Fatal("differing active catalog accepted")
	}
	other := New(t.TempDir())
	defer other.Close()
	if _, _, err := other.Acquire(t.Context(), cwd, []protocol.SkillRef{ref}); !errors.Is(err, ErrBusy) {
		t.Fatal("another owner replaced live copy")
	}
	if err := os.WriteFile(filepath.Join(ref.Path, "SKILL.md"), []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Acquire(t.Context(), cwd, []protocol.SkillRef{ref}); !errors.Is(err, ErrBusy) {
		t.Fatal("changed content reused a live lease")
	}
	data, err := os.ReadFile(filepath.Join(catalog[0].Path, "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "SOURCE_BODY") {
		t.Fatal("live copy was modified")
	}
}

func TestManager_CollisionRenamesOwnedCopyAndNativeManualMetadata(t *testing.T) {
	m, cwd, ref := fixture(t)
	ref.Invocation = protocol.SkillInvocationManual
	existing := filepath.Join(cwd, filepath.FromSlash(projectDir), ref.Name)
	if err := os.MkdirAll(existing, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "SKILL.md"), []byte("user skill"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(ref.Path, "agents"), 0700); err != nil {
		t.Fatal(err)
	}
	original := "interface:\n  display_name: User label\npolicy:\n  products: [codex]\n"
	if err := os.WriteFile(filepath.Join(ref.Path, "agents", "openai.yaml"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, release, err := m.Acquire(t.Context(), cwd, []protocol.SkillRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	if catalog[0].Name != "arvel-review" {
		t.Fatal("SDK collision alias lost")
	}
	metadata, err := os.ReadFile(filepath.Join(catalog[0].Path, "agents", "openai.yaml"))
	if err != nil || !strings.Contains(string(metadata), "allow_implicit_invocation: false") || !strings.Contains(string(metadata), "User label") {
		t.Fatal("native manual policy missing or source metadata lost")
	}
	release()
	current, _ := os.ReadFile(filepath.Join(ref.Path, "agents", "openai.yaml"))
	if string(current) != original {
		t.Fatal("source metadata mutated")
	}
	data, _ := os.ReadFile(filepath.Join(existing, "SKILL.md"))
	if string(data) != "user skill" {
		t.Fatal("user skill removed")
	}
}

func TestManager_RejectsOccupiedAliasesSymlinksAndPrivateSources(t *testing.T) {
	m, cwd, ref := fixture(t)
	for _, name := range []string{ref.Name, "arvel-" + ref.Name} {
		if err := os.MkdirAll(filepath.Join(cwd, filepath.FromSlash(projectDir), name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := m.Acquire(t.Context(), cwd, []protocol.SkillRef{ref}); !errors.Is(err, ErrBusy) {
		t.Fatal("occupied aliases overwritten")
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(ref.Path, "escape")); err == nil {
		if _, _, err := m.Acquire(t.Context(), cwd, []protocol.SkillRef{ref}); err == nil {
			t.Fatal("symlink source accepted")
		}
	}
	m2 := New(ref.Path)
	defer m2.Close()
	if _, _, err := m2.Acquire(t.Context(), cwd, []protocol.SkillRef{ref}); !errors.Is(err, ErrSource) {
		t.Fatal("private source read")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := m.Acquire(ctx, cwd, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled acquisition succeeded")
	}
}

func TestManager_ProviderExitDoesNotProveAnAbandonedCopyIsUnused(t *testing.T) {
	m, cwd, ref := fixture(t)
	catalog, _, err := m.Acquire(t.Context(), cwd, []protocol.SkillRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate process death: kernel lock is released, journal/copies survive.
	<-m.gate
	var originalCleanup func()
	for key, entry := range m.leases {
		originalCleanup = entry.cleanup
		entry.coord.close()
		delete(m.leases, key)
	}
	m.closed = true
	m.gate <- struct{}{}
	if _, err := os.Stat(catalog[0].Path); err != nil {
		t.Fatal(err)
	}
	recovered := New(t.TempDir())
	defer recovered.Close()
	if _, _, err := recovered.Acquire(t.Context(), cwd, []protocol.SkillRef{ref}); !errors.Is(err, ErrBusy) {
		t.Fatal("abandoned copy reclaimed without task-liveness proof")
	}
	if _, err := os.Stat(catalog[0].Path); err != nil {
		t.Fatal("ambiguous owner copy deleted")
	}
	// In this isolated unit fixture there is no CLI task. Its retained SDK cleanup
	// is explicit proof-backed recovery and still knows the unique copy owner.
	originalCleanup()
	next, release, err := recovered.Acquire(t.Context(), cwd, []protocol.SkillRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(next[0].Path, "SKILL.md")); err != nil {
		t.Fatal("journal recovery lost assignment")
	}
	release()
	if _, err := os.Stat(next[0].Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovered owner did not clean its copy")
	}
	if _, err := os.Stat(filepath.Join(ref.Path, "SKILL.md")); err != nil {
		t.Fatal("external source removed during recovery")
	}
}

func TestManager_HostNameChangesOnlyOwnedHeaderAndPreservesBody(t *testing.T) {
	m, cwd, ref := fixture(t)
	original := "---\r\nname: source-name\r\ndescription: Source description.\r\n---\r\nBODY\r\n"
	if err := os.WriteFile(filepath.Join(ref.Path, "SKILL.md"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	ref.Name = "host-name"
	catalog, release, err := m.Acquire(t.Context(), cwd, []protocol.SkillRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	copy, err := os.ReadFile(filepath.Join(catalog[0].Path, "SKILL.md"))
	if err != nil || !strings.Contains(string(copy), "name: host-name\r\n") || !strings.HasSuffix(string(copy), "BODY\r\n") {
		t.Fatal("owned native header or body incorrect")
	}
	source, _ := os.ReadFile(filepath.Join(ref.Path, "SKILL.md"))
	if string(source) != original {
		t.Fatal("source header changed")
	}
}

func TestManager_ChangedSDKOwnerPreservesCopyAndBlocksReplacement(t *testing.T) {
	m, cwd, ref := fixture(t)
	catalog, release, err := m.Acquire(t.Context(), cwd, []protocol.SkillRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catalog[0].Path, marker), []byte("another copy owner"), 0600); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(catalog[0].Path); err != nil {
		t.Fatal("a replacement owner was deleted")
	}
	if _, _, err := m.Acquire(t.Context(), cwd, nil); !errors.Is(err, ErrBusy) {
		t.Fatal("unremoved copy silently treated as disabled")
	}
}
