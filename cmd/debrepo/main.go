// Command debrepo 是一个查询 Debian/Ubuntu（apt）软件仓库的命令行工具，
// 用来演示与排查 debrepo SDK。
//
// 用法示例：
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
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("缺少子命令")
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
		return fmt.Errorf("未知子命令 %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `用法:
  debrepo packages [选项] <仓库地址> <软件名称>
  debrepo sources  [选项] <仓库地址> <源码包名称>
  debrepo release  [选项] <仓库地址>
  debrepo indexes  [选项] <仓库地址>

子命令:
  packages  查询二进制软件包（名称支持 * ? [abc] 通配符）
  sources   查询源码包（Sources 索引）
  release   查看 Release/InRelease 元数据（发行信息与索引文件列表）
  indexes   查看实际使用的 Packages/Sources 索引（组件、架构、压缩格式）

选项:
  -suite string       发行版套件/代号，例如 bookworm、jammy、stable
  -component string   组件，逗号分隔（main、contrib），* 表示全部
  -arch string        架构，逗号分隔（amd64、arm64），* 表示全部
  -version string     限定版本号（Debian 版本字符串）
  -latest             每个 名称+架构 只保留最新版本
  -limit int          最多输出多少条
  -sort string        排序方式：default|version-asc|filename|size|none
  -json               以 JSON 输出完整元数据
  -verify             校验索引校验和
  -by-hash            优先使用 by-hash 地址获取索引
  -timeout duration   HTTP 超时（默认 2m）

示例:
  debrepo packages -suite bookworm https://deb.debian.org/debian nginx
  debrepo packages -suite jammy -arch amd64 -latest -json http://archive.ubuntu.com/ubuntu bash
  debrepo packages -suite bookworm -component '*' -latest https://deb.debian.org/debian docker-ce
  debrepo sources -suite bookworm https://deb.debian.org/debian nginx
  debrepo indexes -suite jammy http://archive.ubuntu.com/ubuntu
`)
}

// commonFlags 是各子命令共用的仓库选择参数。
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
		suite:     flags.String("suite", "", "发行版套件/代号，例如 bookworm、jammy、stable"),
		component: flags.String("component", "", "组件，逗号分隔，* 表示全部"),
		arch:      flags.String("arch", "", "架构，逗号分隔，* 表示全部"),
		verify:    flags.Bool("verify", false, "校验索引校验和"),
		byHash:    flags.Bool("by-hash", false, "优先使用 by-hash 地址"),
		timeout:   flags.Duration("timeout", 2*time.Minute, "HTTP 超时"),
	}
}

// options 把命令行参数转换为 SDK 选项。
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
	version := flags.String("version", "", "限定版本号")
	latest := flags.Bool("latest", false, "只保留最新版本")
	limit := flags.Int("limit", 0, "最多输出多少条")
	sortBy := flags.String("sort", "default", "排序方式")
	asJSON := flags.Bool("json", false, "以 JSON 输出")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		usage()
		return errors.New("packages 子命令需要 <仓库地址> 与 <软件名称> 两个参数")
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

	fmt.Fprintf(os.Stderr, "共 %d 个软件包（仓库 %s，suite %s%s）\n",
		len(pkgs), repo.ID, repo.Suite, releaseInfo(repo))
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "包名\t版本\t架构\t组件\t大小\t指纹\t下载地址")
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
	version := flags.String("version", "", "限定版本号")
	latest := flags.Bool("latest", false, "只保留最新版本")
	limit := flags.Int("limit", 0, "最多输出多少条")
	sortBy := flags.String("sort", "default", "排序方式")
	asJSON := flags.Bool("json", false, "以 JSON 输出")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		usage()
		return errors.New("sources 子命令需要 <仓库地址> 与 <源码包名称> 两个参数")
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

	fmt.Fprintf(os.Stderr, "共 %d 个源码包（仓库 %s，suite %s%s）\n",
		len(sources), repo.ID, repo.Suite, releaseInfo(repo))
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "源码包\t版本\t组件\t目录\t文件数\tdsc 地址")
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
	asJSON := flags.Bool("json", false, "以 JSON 输出")
	limit := flags.Int("limit", 0, "索引文件列表最多输出多少条")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		usage()
		return errors.New("release 子命令需要 <仓库地址> 一个参数")
	}
	repo, err := debrepo.Open(context.Background(), flags.Arg(0), common.options()...)
	if err != nil {
		return err
	}
	if repo.Release == nil {
		return fmt.Errorf("%w: %s 没有 Release/InRelease", debrepo.ErrNotRepository, repo.URL)
	}
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(repo.Release)
	}

	release := repo.Release
	fmt.Printf("仓库: %s\n", repo.URL)
	fmt.Printf("元数据: %s（InRelease: %t）\n", repo.ReleaseURL, repo.InRelease)
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
	fmt.Fprintf(os.Stderr, "\n共 %d 个索引文件，显示 %d 条（每行取最强校验算法）\n",
		len(release.FilesByStrength()), len(files))
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "算法\t大小\t路径")
	for _, file := range files {
		fmt.Fprintf(writer, "%s\t%s\t%s\n", file.Checksum.Type, humanSize(file.Size), file.Path)
	}
	return writer.Flush()
}

func runIndexes(args []string) error {
	flags := flag.NewFlagSet("indexes", flag.ContinueOnError)
	common := registerCommon(flags)
	asJSON := flags.Bool("json", false, "以 JSON 输出")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		usage()
		return errors.New("indexes 子命令需要 <仓库地址> 一个参数")
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

	fmt.Fprintf(os.Stderr, "仓库 %s，suite %s，组件 %s，架构 %s\n",
		repo.ID, repo.Suite, strings.Join(repo.Components, " "), strings.Join(repo.Architectures, " "))
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "类型\t组件\t架构\t压缩\t大小\t候选数\t地址")
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

// releaseInfo 返回 "，codename trixie" 这样的附加信息。
func releaseInfo(repo *debrepo.Repository) string {
	if repo.Codename != "" && !strings.EqualFold(repo.Codename, repo.Suite) {
		return "，codename " + repo.Codename
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
		return 0, fmt.Errorf("未知排序方式 %q", name)
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
