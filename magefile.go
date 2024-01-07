//go:build mage

// +buld mage

package main

import (
	"fmt"
	"log"
	"path/filepath"

	"github.com/carolynvs/magex/pkg"
	"github.com/carolynvs/magex/pkg/archive"
	"github.com/carolynvs/magex/pkg/downloads"
	"github.com/magefile/mage/sh"

	"sigs.k8s.io/release-utils/mage"
)

// Default target to run when none is specified
// If not set, running mage will list available targets
var Default = All

const (
	binDir    = "bin"
	scriptDir = "scripts"
)

var boilerplateDir = filepath.Join(scriptDir, "boilerplate")

// All runs all targets for this repository
func All() error {
	if err := Verify(); err != nil {
		return err
	}

	if err := Test(); err != nil {
		return err
	}

	return nil
}

// Test runs various test functions
func Test() error {
	// if err := mage.TestGo(true); err != nil {
	// return err
	// }

	return nil
}

func init() {
	fmt.Println("Ensuring mage is available...")
	if err := pkg.EnsureMage(""); err != nil {
		log.Fatalf("Mage couldn't be installed: %s", err.Error())
	}

	log.Println("Mage successfully initialized")
}

// Verify runs repository verification scripts
func Verify() error {
	log.Println("Running copyright header checks...")
	if err := mage.VerifyBoilerplate("", binDir, boilerplateDir, false); err != nil {
		return err
	}

	log.Println("Running go module linter...")
	if err := mage.VerifyGoMod(); err != nil {
		return err
	}

	// 	log.Println("Running golangci-lint...")
	// 	if err := mage.RunGolangCILint("", false); err != nil {
	// 		return err
	// 	}

	log.Println("Running go build...")
	if err := mage.VerifyBuild(scriptDir); err != nil {
		return err
	}

	return nil
}

func ReleaseLocal() error {
	return releaseWithGoReleaser(true)
}

func Release() error {
	return releaseWithGoReleaser(false)
}

func releaseWithGoReleaser(snapshot bool) error {
	log.Println("Releasing with goreleaser...")
	if err := EnsureBinary("goreleaser", "-v", "1.20.0"); err != nil {
		return err
	}

	if err := mage.EnsureKO(""); err != nil {
		return fmt.Errorf("KO couldn't be installed: %w", err)
	}

	err := sh.RunV("ko", "login", "dockerhub", "-u", "${{ secrets.DOCKERHUB_USERNAME }}", "-p", "${{ secrets.DOCKERHUB_PASSWORD }}")
	if err != nil {
		return fmt.Errorf("ko login failed")
	}

	args := []string{"release", "--clean"}
	if snapshot {
		args = append(args, "--snapshot")
	}

	return sh.RunV("goreleaser", args...)
}

func EnsureBinary(binary, cmd, version string) error {
	log.Printf("Checking if `%s` version %s is installed\n", binary, version)
	found, err := pkg.IsCommandAvailable(binary, cmd, "")
	if err != nil {
		return err
	}

	if !found {
		log.Printf("`%s` not found\n", binary)
		switch binary {
		case "goreleaser":
			return InstallGoReleaser(version)
		}
	}

	log.Printf("`%s` is installed!\n", binary)
	return nil
}

func InstallGoReleaser(version string) error {
	log.Printf("Will install `goreleaser` version `%s`\n", version)
	target := "goreleaser"
	opts := archive.DownloadArchiveOptions{
		DownloadOptions: downloads.DownloadOptions{
			UrlTemplate: "https://github.com/goreleaser/goreleaser/releases/download/v{{.VERSION}}/goreleaser_{{.GOOS}}_{{.GOARCH}}{{.EXT}}",
			Name:        "goreleaser",
			Version:     version,
			OsReplacement: map[string]string{
				"darwin":  "Darwin",
				"linux":   "Linux",
				"windows": "Windows",
			},
			ArchReplacement: map[string]string{
				"amd64": "x86_64",
			},
		},
		ArchiveExtensions: map[string]string{
			"linux":   ".tar.gz",
			"darwin":  ".tar.gz",
			"windows": ".tar.gz",
		},
		TargetFileTemplate: target,
	}

	return archive.DownloadToGopathBin(opts)
}
