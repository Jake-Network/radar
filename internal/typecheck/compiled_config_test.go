package typecheck

import (
	"context"
	"testing"
)

func TestCompiledConfigurationSelectsOwningBuild(t *testing.T) {
	for _, tc := range []struct {
		name, changed, tool, root string
		files                     tree
	}{
		{"cargo manifest", "core/Cargo.toml", "cargo", ".", tree{"Cargo.toml": "[workspace]\nmembers=[\"core\"]", "core/Cargo.toml": "[package]\nname=\"core\"", "core/src/lib.rs": ""}},
		{"cargo lock", "Cargo.lock", "cargo", ".", tree{"Cargo.toml": "[package]\nname=\"core\"", "Cargo.lock": "", "src/lib.rs": ""}},
		{"maven module", "api/pom.xml", "java", ".", tree{"pom.xml": "<project/>", "api/pom.xml": "<project/>", "api/src/Main.java": ""}},
		{"gradle settings", "settings.gradle.kts", "java", ".", tree{"settings.gradle.kts": "include(\"api\")", "api/build.gradle.kts": "plugins { java }", "api/src/Main.java": ""}},
		{"gradle build", "api/build.gradle", "java", ".", tree{"settings.gradle": "include 'api'", "api/build.gradle": "plugins { java }", "api/src/Main.java": ""}},
		{"gradle properties", "gradle.properties", "java", ".", tree{"gradle.properties": "", "build.gradle": "plugins { java }", "src/Main.java": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Select(context.Background(), tc.files, []string{tc.changed})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Targets) != 1 || p.Targets[0].Tool != tc.tool || p.Targets[0].Root != tc.root || len(p.Gaps) != 0 {
				t.Fatalf("%+v", p)
			}
		})
	}
}

func TestChangedCompilationDatabaseChecksEveryUnit(t *testing.T) {
	files := tree{"native/main.cpp": "", "native/other.cpp": "", "compile_commands.json": `[{"directory":"native","file":"main.cpp","arguments":["g++","-DBREAK","-c","main.cpp"]},{"directory":"native","file":"other.cpp","arguments":["g++","-c","other.cpp"]}]`}
	p, err := Select(context.Background(), files, []string{"compile_commands.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != 2 || len(p.Gaps) != 0 {
		t.Fatalf("%+v", p)
	}
	for _, target := range p.Targets {
		if len(target.Changed) != 1 || !CPPSource(target.Changed[0]) {
			t.Fatalf("source inventory is not required: %+v", target)
		}
	}
}

func TestUnsupportedCompilationDatabaseKeepsGap(t *testing.T) {
	for _, database := range []string{`[]`, `not json`, `[{"directory":"/old/checkout","file":"main.cpp","arguments":["g++","-c","main.cpp"]}]`, `[{"directory":".","file":"main.cpp","arguments":["g++","-E","main.cpp"]}]`, `[{"directory":".","file":"main.cpp","arguments":["g++","-c","main.cpp"]},{"directory":".","file":"other.cpp","arguments":["sh","-c","g++ other.cpp"]}]`} {
		files := tree{"go.mod": "module example.test/repo\n", "main.go": "package repo\n", "main.cpp": "", "other.cpp": "", "compile_commands.json": database}
		p, err := Select(context.Background(), files, []string{"compile_commands.json"})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Gaps) == 0 {
			t.Fatalf("unsupported config lost behind Go target: %+v", p)
		}
	}
}

func TestDeletedCompiledConfigurationKeepsGap(t *testing.T) {
	for _, name := range []string{"Cargo.toml", "Cargo.lock", "pom.xml", "settings.gradle.kts", "build.gradle", "compile_commands.json"} {
		p, err := Select(context.Background(), tree{"README.md": ""}, []string{name})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Gaps) == 0 {
			t.Fatalf("deleted %s silently ignored: %+v", name, p)
		}
	}
}

func TestUnrelatedConfigurationDoesNotSelectCompiledBuild(t *testing.T) {
	p, err := Select(context.Background(), tree{"settings.json": "{}", "config.toml": "", "docs/pom.xml.example": ""}, []string{"settings.json", "config.toml", "docs/pom.xml.example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != 0 || len(p.Gaps) != 0 {
		t.Fatalf("unrelated configuration selected: %+v", p)
	}
}
