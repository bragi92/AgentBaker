package winnote

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

/**
*** This binary autogenerates release notes for AKS VHD releases.
***
*** It accepts:
*** - a run ID from which to download artifacts.
*** - the VHD build date for output naming
*** - a comma-separated list of VHD names to include/ignore.
***
*** Examples:
*** # download ONLY 2019-containerd release notes from this run ID.
*** autonotes --build 76289801 --include 2019-containerd
***
*** # download everything EXCEPT 2022-containerd-gen2 release notes from this run ID.
*** autonotes --build 76289801 --ignore 2022-containerd-gen2
***
*** # download ONLY 2022-containerd,2022-containerd-gen2 release notes from this run ID.
*** autonotes --build 76289801 --include 2022-containerd,2022-containerd-gen2
**/

func main() {
	var fl flags
	flag.StringVar(&fl.build, "build", "", "run ID for the VHD build.")
	flag.StringVar(&fl.include, "include", "", "only include this list of VHD release notes.")
	flag.StringVar(&fl.ignore, "ignore", "", "ignore release notes for these VHDs")
	flag.StringVar(&fl.path, "path", defaultPath, "output path to root of VHD notes")
	flag.StringVar(&fl.date, "date", defaultDate, "date of VHD build in format YYMMDD")

	flag.Parse()

	int := make(chan os.Signal, 1)
	signal.Notify(int, os.Interrupt)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-int; cancel() }()

	if errs := run(ctx, cancel, &fl); errs != nil {
		for _, err := range errs {
			fmt.Println(err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, cancel context.CancelFunc, fl *flags) []error {
	var include, ignore map[string]bool

	includeString := stripWhitespace(fl.include)
	if len(includeString) > 0 {
		include = map[string]bool{}
		includeTokens := strings.Split(includeString, ",")
		for _, token := range includeTokens {
			include[token] = true
		}
	}

	ignoreString := stripWhitespace(fl.ignore)
	if len(ignoreString) > 0 {
		ignore = map[string]bool{}
		ignoreTokens := strings.Split(ignoreString, ",")
		for _, token := range ignoreTokens {
			ignore[token] = true
		}
	}

	enforceInclude := len(include) > 0

	// Get windows base image versions frpm the updated windows-image.env
	var wsImageVersionFilePath = filepath.Join("vhdbuilder", "packer", "windows-image.env")
	file, err := os.Open(wsImageVersionFilePath)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "WINDOWS_2019_BASE_IMAGE_VERSION=") {
			imageVersions["2019-containerd"] = strings.Split(line, "=")[1]
		} else if strings.Contains(line, "WINDOWS_2022_BASE_IMAGE_VERSION=") {
			imageVersions["2022-containerd"] = strings.Split(line, "=")[1]
		} else if strings.Contains(line, "WINDOWS_2022_GEN2_BASE_IMAGE_VERSION=") {
			imageVersions["2022-containerd-gen2"] = strings.Split(line, "=")[1]
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	artifactsToDownload := map[string]string{}
	for key, value := range artifactToPath {
		if ignore[key] {
			continue
		}

		if enforceInclude && !include[key] {
			continue
		}

		artifactsToDownload[key] = value
	}

	var errc = make(chan error)
	var done = make(chan struct{})

	for sku, path := range artifactsToDownload {
		length := len(imageVersions[sku])
		version := imageVersions[sku][0:length - 6] + fl.date
		go getReleaseNotes(sku, path, fl, errc, done, version)
	}

	var errs []error

	for i := 0; i < len(artifactsToDownload); i++ {
		select {
		case err := <-errc:
			errs = append(errs, err)
		case <-done:
			continue
		}
	}

	return errs
}

func getReleaseNotes(sku, path string, fl *flags, errc chan<- error, done chan<- struct{}, version string) {
	defer func() { done <- struct{}{} }()

	// working directory, need one per sku because the file name is
	// always "release-notes.txt" so they all overwrite each other.
	tmpdir, err := ioutil.TempDir("", "releasenotes")
	if err != nil {
		errc <- fmt.Errorf("failed to create temp working directory: %w", err)
	}
	defer os.RemoveAll(tmpdir)

	releaseNotesName := fmt.Sprintf("vhd-release-notes-%s", sku)
	releaseNotesFileIn := filepath.Join(tmpdir, "release-notes.txt")
	imageListName := fmt.Sprintf("vhd-image-bom-%s", sku)
	imageListFileIn := filepath.Join(tmpdir, "image-bom.json")

	artifactsDirOut := filepath.Join(fl.path, path)
	releaseNotesFileOut := filepath.Join(artifactsDirOut, fmt.Sprintf("%s.txt", version))
	imageListFileOut := filepath.Join(artifactsDirOut, fmt.Sprintf("%s-image-list.json", version))

	if err := os.MkdirAll(filepath.Dir(artifactsDirOut), 0644); err != nil {
		errc <- fmt.Errorf("failed to create parent directory %s with error: %s", artifactsDirOut, err)
		return
	}

	if err := os.MkdirAll(artifactsDirOut, 0644); err != nil {
		errc <- fmt.Errorf("failed to create parent directory %s with error: %s", artifactsDirOut, err)
		return
	}

	fmt.Printf("downloading releaseNotes '%s' from build '%s'\n", releaseNotesName, fl.build)

	cmd := exec.Command("az", "pipelines", "runs", "artifact", "download", "--run-id", fl.build, "--path", tmpdir, "--artifact-name", releaseNotesName)
	if stdout, err := cmd.CombinedOutput(); err != nil {
		if err != nil {
			errc <- fmt.Errorf("failed to download az devops releaseNotes for sku %s, err: %s, output: %s", sku, err, string(stdout))
		}
		return
	}

	if err := os.Rename(releaseNotesFileIn, releaseNotesFileOut); err != nil {
		errc <- fmt.Errorf("failed to rename file %s to %s, err: %s", releaseNotesFileIn, releaseNotesFileOut, err)
		return
	}

	cmd = exec.Command("az", "pipelines", "runs", "artifact", "download", "--run-id", fl.build, "--path", tmpdir, "--artifact-name", imageListName)
	if stdout, err := cmd.CombinedOutput(); err != nil {
		if err != nil {
			errc <- fmt.Errorf("failed to download az devops imageList for sku %s, err: %s, output: %s", sku, err, string(stdout))
		}
		return
	}

	if err := os.Rename(imageListFileIn, imageListFileOut); err != nil {
		errc <- fmt.Errorf("failed to rename file %s to %s, err: %s", imageListFileIn, imageListFileOut, err)
		return
	}
}

func stripWhitespace(str string) string {
	var b strings.Builder
	b.Grow(len(str))
	for _, ch := range str {
		if !unicode.IsSpace(ch) {
			b.WriteRune(ch)
		}
	}
	return b.String()
}

type flags struct {
	build   string
	include string // CSV of the map keys below.
	ignore  string // CSV of the map keys below.
	path    string // output path
	date    string // date of vhd build
}

var defaultPath = filepath.Join("vhdbuilder", "release-notes")
var defaultDate = time.Now().Format("060102")
var imageVersions = make(map[string]string)
	
// why does ubuntu use subfolders and mariner doesn't
// there are dependencies on the folder structure but it would
// be nice to fix this.
var artifactToPath = map[string]string{
	"2019-containerd":                filepath.Join("AKSWindows", "2019-containerd"),
	"2022-containerd":                filepath.Join("AKSWindows", "2022-containerd"),
	"2022-containerd-gen2":           filepath.Join("AKSWindows", "2022-containerd-gen2"),	
}
