package testselection

import (
	"context"
	"testing"

	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
)

// An isolated revision has no import relationship to tests, but balanced
// selection deliberately retains optional package-root fallbacks. Targeted
// selection must not turn workspace membership into an import relationship.
func TestPythonWorkspaceIsolatedRevisionOnlyHasOptionalFallbacks(t *testing.T) {
	revision := "apps/qurify/alembic/versions/new_revision.py"
	root := writeTree(t, map[string]string{
		"pyproject.toml":                                               "[tool.uv.workspace]\nmembers = [\"apps/*\", \"libs/*\"]\n",
		"apps/qurify/pyproject.toml":                                   "[project]\nname = 'qurify'\n[tool.pytest.ini_options]\npythonpath = ['.', 'app']\ntestpaths = ['**/tests']\n",
		"apps/qurify/conftest.py":                                      "import pytest\n",
		"apps/qurify/app/api.py":                                       "def run(): return 1\n",
		"apps/qurify/app/tests/test_api.py":                            "from app.api import run\ndef test_run(): assert run() == 1\n",
		"apps/qurify/app/tests/test_other.py":                          "def test_other(): assert True\n",
		"apps/qurify/alembic/env.py":                                   "from app.api import run\n",
		revision:                                                       "from typing import Sequence\nfrom collections.abc import Iterable\nrevision = 'new'\ndown_revision = 'old'\n",
		"libs/shared-modules/pyproject.toml":                           "[project]\nname = 'shared-modules'\n[tool.pytest.ini_options]\ntestpaths = ['src']\n",
		"libs/shared-modules/src/shared_modules/service.py":            "def run(): return 1\n",
		"libs/shared-modules/src/shared_modules/tests/test_service.py": "from shared_modules.service import run\ndef test_run(): assert run() == 1\n",
	})
	inv, err := Discover(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := indexer.Index(context.Background(), root, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range snapshot.Edges {
		if edge.Kind == "DEPENDS_ON" && edge.To == model.FileID(revision) {
			t.Fatalf("isolated revision gained an importer: %+v", edge)
		}
	}
	p, err := Select(inv, snapshot, []string{revision}, Options{ToolAvailable: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Tests) != 3 || len(p.Commands) != 2 {
		t.Fatalf("inventory=%d commands=%+v", len(inv.Tests), p.Commands)
	}
	for _, command := range p.Commands {
		if command.CWD != "apps/qurify" || len(command.EvidenceReasons) != 1 || command.EvidenceReasons[0].Code != "package_fallback" || tierFor(command) != TierOptional {
			t.Fatalf("invented direct relationship: %+v", command)
		}
	}
	selected, err := Plan(p, []string{revision}, ModeTargeted, DefaultMaxCommands)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Commands) != 0 || len(selected.Uncovered) != 1 || selected.Uncovered[0] != revision {
		t.Fatalf("targeted must report an uncovered revision: %+v", selected)
	}
}
