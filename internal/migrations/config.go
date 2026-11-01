package migrations

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

type configuredLocation struct {
	Path      string `json:"path"`
	Tool      string `json:"tool"`
	Group     string `json:"group,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
}
type configuration struct {
	Version   int                  `json:"version"`
	Locations []configuredLocation `json:"locations"`
}

func readManifestConfig(t Tree, read Reader) ([]configuredLocation, bool, error) {
	for _, f := range t.Files {
		if f.Path != ".radar/migrations.json" {
			continue
		}
		b, e := read(f.OID)
		if e != nil || len(b) > maxFileBytes {
			return nil, true, fmt.Errorf("Migration configuration cannot be read within limit")
		}
		var c configuration
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if e = d.Decode(&c); e != nil || c.Version != 1 || len(c.Locations) == 0 || len(c.Locations) > 128 {
			return nil, true, fmt.Errorf("Migration configuration requires version 1 and literal locations")
		}
		var trailing any
		if e = d.Decode(&trailing); e != io.EOF {
			return nil, true, fmt.Errorf("Migration configuration contains trailing data")
		}
		seen := map[string]bool{}
		for i, l := range c.Locations {
			if l.Path == "" || path.IsAbs(l.Path) || strings.Contains(l.Path, "\\") || path.Clean(l.Path) != l.Path || l.Path == ".." || strings.HasPrefix(l.Path, "../") {
				return nil, true, fmt.Errorf("Migration location must stay inside repository")
			}
			if l.Group != "" && (l.Tool != toolAlembic || path.IsAbs(l.Group) || path.Clean(l.Group) != l.Group || l.Group == ".." || strings.HasPrefix(l.Group, "../") || strings.Contains(l.Group, "\\") || !(l.Group == "." || l.Path == l.Group || strings.HasPrefix(l.Path, l.Group+"/"))) {
				return nil, true, fmt.Errorf("Alembic group must be an ancestor repository directory")
			}
			if l.Recursive && l.Tool != toolAlembic {
				return nil, true, fmt.Errorf("Recursive migration locations are supported only for Alembic")
			}
			switch l.Tool {
			case toolAlembic, toolDjango, toolFlyway, toolSQL, toolPrisma, toolLiquibase, toolSQLX, toolDiesel, toolGoose, toolAtlas, toolDrizzle, toolTypeORM, toolKnex:
			case "golang-migrate":
				c.Locations[i].Tool = toolSQL
			default:
				return nil, true, fmt.Errorf("Unsupported migration tool %q", l.Tool)
			}
			if seen[l.Path] {
				return nil, true, fmt.Errorf("Ambiguous duplicate migration location")
			}
			seen[l.Path] = true
		}
		return c.Locations, true, nil
	}
	return nil, false, nil
}

var timestampName = regexp.MustCompile(`^([0-9]+)_[^/]+$`)
var sqlxName = regexp.MustCompile(`^([0-9]+)_[^/]+\.sql$`)

func classifyConfigured(p string, dirs map[string]map[string]bool, configs []configuredLocation) (location, bool) {
	var selected *configuredLocation
	for i := range configs {
		l := &configs[i]
		if l.Path == "." || p == l.Path || strings.HasPrefix(p, l.Path+"/") {
			if selected == nil || len(l.Path) > len(selected.Path) {
				selected = l
			}
		}
	}
	if selected != nil {
		l := location{selected.Tool, selected.Path}
		if selected.Group != "" {
			l.dir = selected.Group
		}
		name := path.Base(p)
		switch l.tool {
		case toolAlembic, toolDjango:
			if !strings.HasSuffix(name, ".py") {
				return location{}, false
			}
		case toolFlyway:
			if !flywayName.MatchString(name) {
				return location{}, false
			}
		case toolSQL:
			if !sqlName.MatchString(name) {
				return location{}, false
			}
		case toolTypeORM, toolKnex:
			if !(strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".mjs") || strings.HasSuffix(name, ".cjs")) {
				return location{}, false
			}
		case toolAtlas, toolDrizzle:
			if !strings.HasSuffix(name, ".sql") && !strings.HasSuffix(name, ".json") && name != "atlas.sum" {
				return location{}, false
			}
		case toolGoose:
			if !sqlxName.MatchString(name) && !strings.HasSuffix(name, ".go") {
				return location{}, false
			}
		case toolSQLX:
			if !sqlxName.MatchString(name) {
				return location{}, false
			}
		case toolDiesel:
			if name != "up.sql" && name != "down.sql" {
				return location{}, false
			}
		case toolPrisma:
			if name != "migration.sql" {
				return location{}, false
			}
		case toolLiquibase:
			if !strings.HasSuffix(name, ".xml") {
				return location{}, false
			}
		}
		return l, true
	}
	if dirs[path.Dir(p)]["atlas.sum"] {
		return location{toolAtlas, path.Dir(p)}, true
	}
	for dir := path.Dir(p); ; dir = path.Dir(dir) {
		if dirs[path.Join(dir, "meta")]["_journal.json"] {
			return location{toolDrizzle, dir}, true
		}
		if dir == "." {
			break
		}
	}
	if path.Base(p) == "migration.sql" && timestampName.MatchString(path.Base(path.Dir(p))) && path.Base(path.Dir(path.Dir(p))) == "migrations" {
		return location{toolPrisma, path.Dir(path.Dir(p))}, true
	}
	if (path.Base(p) == "up.sql" || path.Base(p) == "down.sql") && timestampName.MatchString(path.Base(path.Dir(p))) && path.Base(path.Dir(path.Dir(p))) == "migrations" {
		return location{toolDiesel, path.Dir(path.Dir(p))}, true
	}
	if sqlxName.MatchString(path.Base(p)) && path.Base(path.Dir(p)) == "migrations" {
		return location{toolSQLX, path.Dir(p)}, true
	}
	return classify(p, dirs)
}
func (h *history) liquibaseIssues(l location, files []File, read Reader) {
	h.contents = map[string]map[string]bool{}
	byKey := map[string][]string{}
	memberPaths := map[string]string{}
	lines := map[string]int{}
	for _, f := range files {
		b, e := read(f.OID)
		if e != nil || len(b) > maxFileBytes {
			h.unknown = append(h.unknown, f.Path+": cannot read XML changelog")
			continue
		}
		d := xml.NewDecoder(bytes.NewReader(b))
		for {
			token, e := d.Token()
			if e == io.EOF {
				break
			}
			if e != nil {
				h.unknown = append(h.unknown, f.Path+": malformed XML changelog")
				break
			}
			start, ok := token.(xml.StartElement)
			if !ok {
				continue
			}
			if start.Name.Local == "include" || start.Name.Local == "includeAll" {
				h.unknown = append(h.unknown, f.Path+": include-based loading is not resolved")
				continue
			}
			if start.Name.Local != "changeSet" {
				continue
			}
			id, author := "", ""
			for _, a := range start.Attr {
				switch a.Name.Local {
				case "id":
					id = a.Value
				case "author":
					author = a.Value
				case "logicalFilePath":
					h.unknown = append(h.unknown, f.Path+": logicalFilePath scope is not resolved")
				}
			}
			if id == "" || author == "" || strings.Contains(id+author, "${") {
				h.unknown = append(h.unknown, f.Path+": nonliteral changeSet identity")
				continue
			}
			keyBytes, _ := json.Marshal([]string{id, author})
			key := string(keyBytes)
			member := fmt.Sprintf("%s#%s#%d", f.Path, key, len(byKey[key]))
			h.members[member] = true
			memberPaths[member] = f.Path
			lines[member] = 1 + bytes.Count(b[:d.InputOffset()], []byte("\n"))
			byKey[key] = append(byKey[key], member)
		}
	}
	for _, key := range sortedKeys(keysOf(byKey)) {
		members := byKey[key]
		if len(members) < 2 {
			continue
		}
		filesForKey := map[string]bool{}
		for _, m := range members {
			filesForKey[memberPaths[m]] = true
		}
		if len(filesForKey) > 1 {
			h.unknown = append(h.unknown, "changeSet "+key+" repeats across files; Liquibase identity also includes the changelog path")
			continue
		}
		is := issue{Key: "changeset:" + key, Kind: kindVersion, Parent: key, Members: members, Paths: map[string]string{}, Lines: map[string]int{}}
		for _, m := range members {
			is.Paths[m] = memberPaths[m]
			is.Lines[m] = lines[m]
			h.contents[m] = map[string]bool{m: true}
		}
		h.issues = append(h.issues, is)
	}
}
func combinedConfig(base, current []configuredLocation) ([]configuredLocation, error) {
	byPath := map[string]configuredLocation{}
	out := []configuredLocation{}
	for _, c := range append(append([]configuredLocation{}, base...), current...) {
		if before, ok := byPath[c.Path]; ok {
			if before != c {
				return nil, fmt.Errorf("Migration tool changed for %s; base obligations cannot be reinterpreted", c.Path)
			}
			continue
		}
		byPath[c.Path] = c
		out = append(out, c)
	}
	return out, nil
}
