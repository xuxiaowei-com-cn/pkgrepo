// Command rpmrepo 是一个查询 RPM 仓库的命令行工具，用来演示与排查 rpmrepo SDK。
//
// 用法示例：
//
//	rpmrepo packages https://download.docker.com/linux/centos/7/x86_64/stable docker-ce
//	rpmrepo packages --arch x86_64 --latest --json <仓库地址> docker-ce
//	rpmrepo repomd <仓库地址>
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/xuxiaowei-com-cn/pkgrepo/rpmrepo"
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
	case "repomd":
		return runRepoMD(args[1:])
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
  rpmrepo packages [选项] <仓库地址> <软件名称>
  rpmrepo repomd   [选项] <仓库地址>

子命令:
  packages  查询指定软件（名称支持 * ? [abc] 通配符）的全部软件包
  repomd    查看仓库元数据索引（repomd.xml）

选项（packages）:
  -arch string      限定架构，例如 x86_64、noarch、src
  -version string   限定版本号（version）
  -latest           每个 名称+架构 只保留最新版本
  -limit int        最多输出多少个包
  -sort string      排序方式：default|version-asc|build-time|filename|none
  -json             以 JSON 输出完整元数据
  -verify           校验元数据校验和
  -timeout duration HTTP 超时（默认 2m）

示例:
  rpmrepo packages https://download.docker.com/linux/centos/7/x86_64/stable docker-ce
  rpmrepo packages -arch x86_64 -latest -json https://download.docker.com/linux/centos/7/x86_64/stable docker-ce
`)
}

func runPackages(args []string) error {
	flags := flag.NewFlagSet("packages", flag.ContinueOnError)
	arch := flags.String("arch", "", "限定架构")
	version := flags.String("version", "", "限定版本号")
	latest := flags.Bool("latest", false, "只保留最新版本")
	limit := flags.Int("limit", 0, "最多输出多少个包")
	sortBy := flags.String("sort", "default", "排序方式")
	asJSON := flags.Bool("json", false, "以 JSON 输出")
	verify := flags.Bool("verify", false, "校验元数据校验和")
	timeout := flags.Duration("timeout", 2*time.Minute, "HTTP 超时")
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
	query := rpmrepo.Query{
		Name:    name,
		Arch:    *arch,
		Version: *version,
		Latest:  *latest,
		Limit:   *limit,
		Sort:    order,
	}
	ctx := context.Background()
	client := rpmrepo.New(rpmrepo.WithTimeout(*timeout), rpmrepo.WithChecksumVerification(*verify))
	repo, err := client.Open(ctx, repoURL)
	if err != nil {
		return err
	}
	pkgs, err := repo.FindPackages(ctx, query)
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

	fmt.Fprintf(os.Stderr, "共 %d 个软件包（仓库 %s，revision %s）\n", len(pkgs), repo.ID, repo.Revision)
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NEVRA\t大小\t构建时间\t指纹\t下载地址")
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

func runRepoMD(args []string) error {
	flags := flag.NewFlagSet("repomd", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "以 JSON 输出")
	timeout := flags.Duration("timeout", 2*time.Minute, "HTTP 超时")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		usage()
		return errors.New("repomd 子命令需要 <仓库地址> 一个参数")
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

	fmt.Printf("仓库: %s\nrevision: %s\n", repo.URL, repo.Revision)
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "类型\t压缩\t大小\t解压后\t生成时间\t地址")
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

func shortChecksum(checksum rpmrepo.Checksum) string {
	if checksum.Value == "" {
		return "-"
	}
	if len(checksum.Value) <= 12 {
		return checksum.Value
	}
	return checksum.Value[:12] + "…"
}
