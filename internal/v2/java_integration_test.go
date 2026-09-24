package v2

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/code-context/internal/config"
	"github.com/example/code-context/internal/lsp"
	"github.com/example/code-context/internal/repository"
	"github.com/example/code-context/internal/tools"
)

func TestRealJDTMultiModuleAndOverload(t *testing.T) {
	binary := os.Getenv("CODE_CONTEXT_JDTLS_BIN")
	if binary == "" {
		t.Skip("set CODE_CONTEXT_JDTLS_BIN for JDT LS integration")
	}
	if _, err := exec.LookPath("mvn"); err != nil {
		t.Skip("Maven is required for the multi-module JDT LS integration fixture")
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	files := map[string]string{
		"pom.xml":                           `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>demo</groupId><artifactId>parent</artifactId><version>1.0</version><packaging>pom</packaging><modules><module>a</module><module>b</module></modules></project>`,
		"a/pom.xml":                         `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><parent><groupId>demo</groupId><artifactId>parent</artifactId><version>1.0</version></parent><artifactId>a</artifactId></project>`,
		"b/pom.xml":                         `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><parent><groupId>demo</groupId><artifactId>parent</artifactId><version>1.0</version></parent><artifactId>b</artifactId><dependencies><dependency><groupId>demo</groupId><artifactId>a</artifactId><version>1.0</version></dependency></dependencies></project>`,
		"a/src/main/java/demo/Service.java": "package demo;\npublic interface Service {\n String run(String value);\n}\n",
		"b/src/main/java/demo/Impl.java":    "package demo;\npublic class Impl implements Service {\n @Override public String run(String value) { return value; }\n public String run(int value) { return \"\" + value; }\n public String call() { return run(\"x\"); }\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	buildCtx, stopBuild := context.WithTimeout(context.Background(), 90*time.Second)
	defer stopBuild()
	build := exec.CommandContext(buildCtx, "mvn", "-q", "-DskipTests", "package")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build multi-module fixture: %v: %s", err, output)
	}
	var cfg config.Config
	cfg.Repositories = map[string]config.Repository{"fixture": {Path: root}}
	repos := repository.New(cfg)
	jdt := lsp.NewJDT(binary, nil, filepath.Join(t.TempDir(), "jdt"))
	defer jdt.Shutdown(context.Background())
	legacy := &tools.Service{Repos: repos, JDT: jdt, MaxResults: 100, MaxCallDepth: 10}
	service := &Service{Legacy: legacy}
	repo, _ := repos.Get("fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	impl := files["b/src/main/java/demo/Impl.java"]
	implLines := strings.Split(impl, "\n")
	col := strings.Index(implLines[1], "Service") + 1
	var definition any
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		definition, err = service.java(ctx, repo, "find_definition", Request{RepoID: "fixture", File: "b/src/main/java/demo/Impl.java", Line: 2, Column: col})
		if err == nil && len(definition.([]tools.Location)) > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	locations := definition.([]tools.Location)
	if len(locations) == 0 || !strings.HasSuffix(locations[0].File, "a/src/main/java/demo/Service.java") {
		t.Fatalf("multi-module definition: %#v", locations)
	}
	runCol := strings.Index(implLines[4], "run(") + 1
	overloaded, err := service.java(ctx, repo, "find_definition", Request{RepoID: "fixture", File: "b/src/main/java/demo/Impl.java", Line: 5, Column: runCol})
	if err != nil {
		t.Fatal(err)
	}
	methodLocations := overloaded.([]tools.Location)
	if len(methodLocations) == 0 || methodLocations[0].StartLine != 3 {
		t.Fatalf("overloaded method: %#v", methodLocations)
	}
}
