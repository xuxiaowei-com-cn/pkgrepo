// Command rpmrepo is a command-line tool for querying RPM repositories, used to demonstrate and
// troubleshoot the rpmrepo SDK.
//
// Usage examples:
//
//	rpmrepo packages https://download.docker.com/linux/centos/7/x86_64/stable docker-ce
//	rpmrepo packages https://repo.almalinux.org/almalinux/9/BaseOS/x86_64/os https://repo.almalinux.org/almalinux/9/AppStream/x86_64/os nginx
//	rpmrepo packages --arch x86_64 --latest --json <repository URL> docker-ce
//	rpmrepo repomd <repository URL>
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

	"github.com/xuxiaowei-com-cn/pkgrepo/rpmrepo"
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
	case "repomd":
		return runRepoMD(args[1:])
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
  rpmrepo packages [options] <repository URL> [<repository URL>...] <package name>
  rpmrepo repomd   [options] <repository URL>

Subcommands:
  packages  list all packages of the given software (the name supports the * ? [abc] wildcards)
  repomd    show the repository metadata index (repomd.xml)

Several repository URLs can be given; every repository is read and the results are merged into a
single list (sorting, -latest, and -limit then apply to the merged list).

Options (packages):
  -arch string      restrict the architecture, for example x86_64, noarch, or src
  -version string   restrict the version number
  -latest           keep only the newest version of every name+architecture
  -limit int        maximum number of packages to output
  -sort string      sort order: default|version-asc|build-time|filename|none
  -json             output the complete metadata as JSON
  -verify           verify metadata checksums
  -timeout duration HTTP timeout (default 2m)

Examples:
  rpmrepo packages https://download.docker.com/linux/centos/7/x86_64/stable docker-ce
  rpmrepo packages https://repo.almalinux.org/almalinux/9/BaseOS/x86_64/os https://repo.almalinux.org/almalinux/9/AppStream/x86_64/os nginx
  rpmrepo packages -arch x86_64 -latest -json https://download.docker.com/linux/centos/7/x86_64/stable docker-ce
`)
}

// splitRepositories splits the positional arguments into repository URLs and the trailing package
// name. Every argument before the last one is a repository URL, so an argument that cannot address a
// repository (a package name given in the wrong position) is reported as a usage error.
func splitRepositories(args []string) ([]string, string, error) {
	repoURLs, name := args[:len(args)-1], args[len(args)-1]
	for _, repoURL := range repoURLs {
		if looksLikeRepository(repoURL) {
			continue
		}
		return nil, "", fmt.Errorf("%q is not a repository address; the last argument is the package name and every argument before it must be a repository URL", repoURL)
	}
	return repoURLs, name, nil
}

// looksLikeRepository reports whether an argument can address a repository: it carries a scheme, it
// contains a path separator, or it is an existing local directory.
func looksLikeRepository(arg string) bool {
	if strings.Contains(arg, "://") || strings.ContainsAny(arg, `/\`) {
		return true
	}
	info, err := os.Stat(arg)
	return err == nil && info.IsDir()
}

func runPackages(args []string) error {
	flags := flag.NewFlagSet("packages", flag.ContinueOnError)
	arch := flags.String("arch", "", "restrict the architecture")
	version := flags.String("version", "", "restrict the version number")
	latest := flags.Bool("latest", false, "keep only the newest version")
	limit := flags.Int("limit", 0, "maximum number of packages to output")
	sortBy := flags.String("sort", "default", "sort order")
	asJSON := flags.Bool("json", false, "output as JSON")
	verify := flags.Bool("verify", false, "verify metadata checksums")
	timeout := flags.Duration("timeout", 2*time.Minute, "HTTP timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() < 2 {
		usage()
		return errors.New("the packages subcommand requires at least one <repository URL> and a <package name>")
	}
	repoURLs, name, err := splitRepositories(flags.Args())
	if err != nil {
		usage()
		return err
	}

	order, err := parseSortOrder(*sortBy)
	if err != nil {
		return err
	}
	query := rpmrepo.Query{
		Name:    name,
		Arch:    *arch,
		Version: *version,
		Latest:  *latest,
		Limit:   *limit,
		Sort:    order,
	}
	ctx := context.Background()
	client := rpmrepo.New(rpmrepo.WithTimeout(*timeout), rpmrepo.WithChecksumVerification(*verify),
		rpmrepo.WithRepositories(repoURLs[1:]...))
	pkgs, summary, err := findPackages(ctx, client, repoURLs, query)
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("%w: %s", rpmrepo.ErrPackageNotFound, name)
	}
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(pkgs)
	}

	fmt.Fprintf(os.Stderr, "%d packages (%s)\n", len(pkgs), summary)
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NEVRA\tSIZE\tBUILD TIME\tCHECKSUM\tDOWNLOAD URL")
	for i := range pkgs {
		pkg := &pkgs[i]
		build := "-"
		if !pkg.Time.Build.IsZero() {
			build = pkg.Time.Build.Time().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
			pkg.NEVRA(), humanSize(pkg.Size.Package), build, shortChecksum(pkg.Checksum), pkg.DownloadURL)
	}
	return writer.Flush()
}

// findPackages reads the given repositories and returns the matching packages together with a
// summary of what was read. A single repository keeps the detailed summary (id and revision), while
// several repositories list their addresses.
func findPackages(ctx context.Context, client *rpmrepo.Client, repoURLs []string, q rpmrepo.Query) ([]rpmrepo.Package, string, error) {
	if len(repoURLs) == 1 {
		repo, err := client.Open(ctx, repoURLs[0])
		if err != nil {
			return nil, "", err
		}
		pkgs, err := repo.FindPackages(ctx, q)
		if err != nil {
			return nil, "", err
		}
		return pkgs, fmt.Sprintf("repository %s, revision %s", repo.ID, repo.Revision), nil
	}
	pkgs, err := client.FindPackages(ctx, repoURLs[0], q)
	if err != nil {
		return nil, "", err
	}
	return pkgs, fmt.Sprintf("repositories %s", strings.Join(repoURLs, ", ")), nil
}

func runRepoMD(args []string) error {
	flags := flag.NewFlagSet("repomd", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "output as JSON")
	timeout := flags.Duration("timeout", 2*time.Minute, "HTTP timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		usage()
		return errors.New("the repomd subcommand requires a single <repository URL> argument")
	}
	repo, err := rpmrepo.Open(context.Background(), flags.Arg(0), rpmrepo.WithTimeout(*timeout))
	if err != nil {
		return err
	}
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(repo.RepoMD)
	}

	fmt.Printf("repository: %s\nrevision: %s\n", repo.URL, repo.Revision)
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "TYPE\tCOMPRESSION\tSIZE\tOPEN SIZE\tTIMESTAMP\tLOCATION")
	for i := range repo.RepoMD.Data {
		data := &repo.RepoMD.Data[i]
		generated := "-"
		if !data.Timestamp.IsZero() {
			generated = data.Timestamp.Time().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(writer, "%s\t%s\t%d\t%d\t%s\t%s\n",
			data.Type, rpmrepo.CompressionOf(data.Location.Href),
			data.Size, data.OpenSize, generated, data.Location.Href)
	}
	return writer.Flush()
}

func parseSortOrder(name string) (rpmrepo.SortOrder, error) {
	switch name {
	case "", "default":
		return rpmrepo.SortDefault, nil
	case "version-asc":
		return rpmrepo.SortVersionAsc, nil
	case "build-time":
		return rpmrepo.SortBuildTimeDesc, nil
	case "filename":
		return rpmrepo.SortFilenameAsc, nil
	case "none":
		return rpmrepo.SortNone, nil
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

func shortChecksum(checksum rpmrepo.Checksum) string {
	if checksum.Value == "" {
		return "-"
	}
	if len(checksum.Value) <= 12 {
		return checksum.Value
	}
	return checksum.Value[:12] + "…"
}
