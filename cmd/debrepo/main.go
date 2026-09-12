// Command debrepo is a command-line tool for querying Debian/Ubuntu (apt) software repositories, used
// to demonstrate and troubleshoot the debrepo SDK.
//
// Usage examples:
//
//	debrepo packages -suite bookworm -arch amd64 https://deb.debian.org/debian nginx
//	debrepo packages -suite jammy -latest -json http://archive.ubuntu.com/ubuntu bash
//	debrepo sources -suite bookworm https://deb.debian.org/debian nginx
//	debrepo release -suite bookworm https://deb.debian.org/debian
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/xuxiaowei-com-cn/pkgrepo/debrepo"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("missing subcommand")
	}
	switch args[0] {
	case "packages", "list":
		return runPackages(args[1:])
	case "sources":
		return runSources(args[1:])
	case "release":
		return runRelease(args[1:])
	case "indexes":
		return runIndexes(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage:
  debrepo packages [options] <repository URL> <package name>
  debrepo sources  [options] <repository URL> <source package name>
  debrepo release  [options] <repository URL>
  debrepo indexes  [options] <repository URL>

Subcommands:
  packages  query binary packages (the name supports the * ? [abc] wildcards)
  sources   query source packages (the Sources index)
  release   show the Release/InRelease metadata (distribution information and index file list)
  indexes   show the Packages/Sources indexes actually used (component, architecture, compression)

Options:
  -suite string       distribution suite/codename, for example bookworm, jammy, or stable
  -component string   components, comma separated (main, contrib); * means all
  -arch string        architectures, comma separated (amd64, arm64); * means all
  -version string     restrict the version number (a Debian version string)
  -latest             keep only the newest version of every name+architecture
  -limit int          maximum number of entries to output
  -sort string        sort order: default|version-asc|filename|size|none
  -json               output the complete metadata as JSON
  -verify             verify index checksums
  -by-hash            fetch indexes from by-hash addresses first
  -timeout duration   HTTP timeout (default 2m)

Examples:
  debrepo packages -suite bookworm https://deb.debian.org/debian nginx
  debrepo packages -suite jammy -arch amd64 -latest -json http://archive.ubuntu.com/ubuntu bash
  debrepo packages -suite bookworm -component '*' -latest https://deb.debian.org/debian docker-ce
  debrepo sources -suite bookworm https://deb.debian.org/debian nginx
  debrepo indexes -suite jammy http://archive.ubuntu.com/ubuntu
`)
}

// commonFlags holds the repository selection flags shared by the subcommands.
type commonFlags struct {
	suite     *string
	component *string
	arch      *string
	verify    *bool
	byHash    *bool
	timeout   *time.Duration
}

func registerCommon(flags *flag.FlagSet) *commonFlags {
	return &commonFlags{
		suite:     flags.String("suite", "", "distribution suite/codename, for example bookworm, jammy, or stable"),
		component: flags.String("component", "", "components, comma separated; * means all"),
		arch:      flags.String("arch", "", "architectures, comma separated; * means all"),
		verify:    flags.Bool("verify", false, "verify index checksums"),
		byHash:    flags.Bool("by-hash", false, "prefer by-hash addresses"),
		timeout:   flags.Duration("timeout", 2*time.Minute, "HTTP timeout"),
	}
}

// options converts the command-line flags into SDK options.
func (f *commonFlags) options() []debrepo.Option {
	options := []debrepo.Option{
		debrepo.WithTimeout(*f.timeout),
		debrepo.WithChecksumVerification(*f.verify),
		debrepo.WithByHashFirst(*f.byHash),
	}
	if strings.TrimSpace(*f.suite) != "" {
		options = append(options, debrepo.WithSuite(strings.TrimSpace(*f.suite)))
	}
	if components := splitFlag(*f.component); len(components) > 0 {
		options = append(options, debrepo.WithComponent(components...))
	}
	if architectures := splitFlag(*f.arch); len(architectures) > 0 {
		options = append(options, debrepo.WithArchitecture(architectures...))
	}
	return options
}

func splitFlag(value string) []string {
	var items []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func runPackages(args []string) error {
	flags := flag.NewFlagSet("packages", flag.ContinueOnError)
	common := registerCommon(flags)
	version := flags.String("version", "", "restrict the version number")
	latest := flags.Bool("latest", false, "keep only the newest version")
	limit := flags.Int("limit", 0, "maximum number of entries to output")
	sortBy := flags.String("sort", "default", "sort order")
	asJSON := flags.Bool("json", false, "output as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		usage()
		return errors.New("the packages subcommand requires both <repository URL> and <package name>")
	}
	repoURL, name := flags.Arg(0), flags.Arg(1)
	order, err := parseSortOrder(*sortBy)
	if err != nil {
		return err
	}
	query := debrepo.Query{
		Name:    name,
		Version: *version,
		Latest:  *latest,
		Limit:   *limit,
		Sort:    order,
	}
	ctx := context.Background()
	repo, err := debrepo.Open(ctx, repoURL, common.options()...)
	if err != nil {
		return err
	}
	pkgs, err := repo.FindPackages(ctx, query)
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("%w: %s", debrepo.ErrPackageNotFound, name)
	}
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(pkgs)
	}

	fmt.Fprintf(os.Stderr, "%d packages (repository %s, suite %s%s)\n",
		len(pkgs), repo.ID, repo.Suite, releaseInfo(repo))
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tVERSION\tARCH\tCOMPONENT\tSIZE\tCHECKSUM\tDOWNLOAD URL")
	for i := range pkgs {
		pkg := &pkgs[i]
		checksum := "-"
		if value, ok := pkg.Checksum(); ok {
			checksum = shortChecksum(value)
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			pkg.Name, pkg.Version.String(), pkg.Architecture, pkg.Component,
			humanSize(pkg.Size.File), checksum, pkg.DownloadURL)
	}
	return writer.Flush()
}

func runSources(args []string) error {
	flags := flag.NewFlagSet("sources", flag.ContinueOnError)
	common := registerCommon(flags)
	version := flags.String("version", "", "restrict the version number")
	latest := flags.Bool("latest", false, "keep only the newest version")
	limit := flags.Int("limit", 0, "maximum number of entries to output")
	sortBy := flags.String("sort", "default", "sort order")
	asJSON := flags.Bool("json", false, "output as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		usage()
		return errors.New("the sources subcommand requires both <repository URL> and <source package name>")
	}
	repoURL, name := flags.Arg(0), flags.Arg(1)
	order, err := parseSortOrder(*sortBy)
	if err != nil {
		return err
	}
	query := debrepo.SourceQuery{
		Name:    name,
		Version: *version,
		Latest:  *latest,
		Limit:   *limit,
		Sort:    order,
	}
	ctx := context.Background()
	repo, err := debrepo.Open(ctx, repoURL, common.options()...)
	if err != nil {
		return err
	}
	sources, err := repo.FindSources(ctx, query)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fmt.Errorf("%w: %s", debrepo.ErrPackageNotFound, name)
	}
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(sources)
	}

	fmt.Fprintf(os.Stderr, "%d source packages (repository %s, suite %s%s)\n",
		len(sources), repo.ID, repo.Suite, releaseInfo(repo))
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "SOURCE\tVERSION\tCOMPONENT\tDIRECTORY\tFILES\tDSC URL")
	for i := range sources {
		source := &sources[i]
		dscURL := "-"
		if dsc, ok := source.DSC(); ok {
			if resolved, err := source.FileURL(dsc.Path); err == nil {
				dscURL = resolved
			}
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%d\t%s\n",
			source.Name, source.Version.String(), source.Component,
			source.Directory, len(source.Files), dscURL)
	}
	return writer.Flush()
}

func runRelease(args []string) error {
	flags := flag.NewFlagSet("release", flag.ContinueOnError)
	common := registerCommon(flags)
	asJSON := flags.Bool("json", false, "output as JSON")
	limit := flags.Int("limit", 0, "maximum number of index file entries to output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		usage()
		return errors.New("the release subcommand requires a single <repository URL> argument")
	}
	repo, err := debrepo.Open(context.Background(), flags.Arg(0), common.options()...)
	if err != nil {
		return err
	}
	if repo.Release == nil {
		return fmt.Errorf("%w: %s has no Release/InRelease", debrepo.ErrNotRepository, repo.URL)
	}
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(repo.Release)
	}

	release := repo.Release
	fmt.Printf("repository: %s\n", repo.URL)
	fmt.Printf("metadata: %s (InRelease: %t)\n", repo.ReleaseURL, repo.InRelease)
	fmt.Printf("Origin: %s\nLabel: %s\nSuite: %s\nCodename: %s\nVersion: %s\n",
		release.Origin, release.Label, release.Suite, release.Codename, release.Version)
	fmt.Printf("Date: %s\nValid-Until: %s\nAcquire-By-Hash: %t\n",
		release.Date, release.ValidUntil, release.AcquireByHash)
	fmt.Printf("Components: %s\nArchitectures: %s\n",
		strings.Join(release.Components, " "), strings.Join(release.Architectures, " "))

	files := release.FilesByStrength()
	if *limit > 0 && len(files) > *limit {
		files = files[:*limit]
	}
	fmt.Fprintf(os.Stderr, "\n%d index files in total, showing %d entries (the strongest checksum algorithm is used per line)\n",
		len(release.FilesByStrength()), len(files))
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ALGORITHM\tSIZE\tPATH")
	for _, file := range files {
		fmt.Fprintf(writer, "%s\t%s\t%s\n", file.Checksum.Type, humanSize(file.Size), file.Path)
	}
	return writer.Flush()
}

func runIndexes(args []string) error {
	flags := flag.NewFlagSet("indexes", flag.ContinueOnError)
	common := registerCommon(flags)
	asJSON := flags.Bool("json", false, "output as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		usage()
		return errors.New("the indexes subcommand requires a single <repository URL> argument")
	}
	repo, err := debrepo.Open(context.Background(), flags.Arg(0), common.options()...)
	if err != nil {
		return err
	}
	indexes := append(repo.Indexes(), repo.SourceIndexes()...)
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(indexes)
	}

	fmt.Fprintf(os.Stderr, "repository %s, suite %s, components %s, architectures %s\n",
		repo.ID, repo.Suite, strings.Join(repo.Components, " "), strings.Join(repo.Architectures, " "))
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "TYPE\tCOMPONENT\tARCH\tCOMPRESSION\tSIZE\tCANDIDATES\tURL")
	for _, index := range indexes {
		architecture := index.Architecture
		if architecture == "" {
			architecture = "-"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			index.Kind, index.Component, architecture,
			debrepo.CompressionOf(index.Path), humanSize(index.Size),
			len(index.Candidates), index.URL)
	}
	return writer.Flush()
}

// releaseInfo returns extra information such as ", codename trixie".
func releaseInfo(repo *debrepo.Repository) string {
	if repo.Codename != "" && !strings.EqualFold(repo.Codename, repo.Suite) {
		return ", codename " + repo.Codename
	}
	return ""
}

func parseSortOrder(name string) (debrepo.SortOrder, error) {
	switch name {
	case "", "default":
		return debrepo.SortDefault, nil
	case "version-asc":
		return debrepo.SortVersionAsc, nil
	case "filename":
		return debrepo.SortFilenameAsc, nil
	case "size":
		return debrepo.SortSizeDesc, nil
	case "none":
		return debrepo.SortNone, nil
	default:
		return 0, fmt.Errorf("unknown sort order %q", name)
	}
}

func humanSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for size/div >= unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGT"[exp])
}

func shortChecksum(checksum debrepo.Checksum) string {
	if checksum.Value == "" {
		return "-"
	}
	if len(checksum.Value) <= 12 {
		return checksum.Value
	}
	return checksum.Value[:12] + "…"
}
