// nolint
package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/loft-sh/log"
	"github.com/loft-sh/vcluster/cmd/vclusterctl/cmd"
	"github.com/spf13/cobra/doc"
)

const cliDocsDir = "./docs/pages/cli"
const headerTemplate = `---
title: "%s --help"
sidebar_label: %s
---

`

const proHeaderTemplate = `---
title: "%[1]s --help"
sidebar_label: %[2]s
sidebar_class_name: "pro-feature-sidebar-item"
---

:::info Note:
` + "`%[1]s`" + ` is only available in the enterprise-ready [vCluster.Pro](https://vcluster.pro) offering.
:::

`

var fixSynopsisRegexp = regexp.MustCompile("(?si)(## vcluster.*?\n)(.*?)#(## Synopsis\n*\\s*)(.*?)(\\s*\n\n\\s*)((```)(.*?))?#(## Options)(.*?)((### Options inherited from parent commands)(.*?)#(## See Also)(\\s*\\* \\[vcluster][^\n]*)?(.*))|(#(## See Also)(\\s*\\* \\[vcluster][^\n]*)?(.*))\n###### Auto generated by spf13/cobra on .*$")

// Run executes the command logic
func main() {
	logger := log.GetInstance()
	filePrepender := func(filename string) string {
		name := filepath.Base(filename)
		base := strings.TrimSuffix(name, path.Ext(name))
		command := strings.Split(base, "_")
		title := strings.Join(command, " ")
		sidebarLabel := title
		l := len(command)

		if l > 1 {
			matches, err := filepath.Glob(cliDocsDir + "/vcluster_" + command[1])
			if err != nil {
				logger.Fatal(err)
			}

			if len(matches) > 2 {
				sidebarLabel = command[l-1]
			}
		}

		if strings.HasPrefix(name, "vcluster_pro") {
			return fmt.Sprintf(proHeaderTemplate, title, sidebarLabel)
		}

		return fmt.Sprintf(headerTemplate, title, sidebarLabel)
	}

	linkHandler := func(name string) string {
		base := strings.TrimSuffix(name, path.Ext(name))
		return strings.ToLower(base) + ".md"
	}

	rootCmd, err := cmd.BuildRoot(logger)
	if err != nil {
		logger.Fatal(err)
	}

	err = doc.GenMarkdownTreeCustom(rootCmd, cliDocsDir, filePrepender, linkHandler)
	if err != nil {
		logger.Fatal(err)
	}

	err = filepath.Walk(cliDocsDir, func(path string, info os.FileInfo, err error) error {
		stat, err := os.Stat(path)
		if stat.IsDir() {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		newContents := fixSynopsisRegexp.ReplaceAllString(string(content), "$2$3$7$8```\n$4\n```\n\n\n## Flags$10\n## Global & Inherited Flags$13")

		err = os.WriteFile(path, []byte(newContents), 0)
		if err != nil {
			return err
		}

		if info.Name() == "vcluster.md" {
			os.Rename(path, filepath.Join(cliDocsDir, "..", "cli.md"))
		}

		return nil
	})
	if err != nil {
		logger.Fatal(err)
	}
}
