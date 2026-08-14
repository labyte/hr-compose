package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"hr.compose/internal/config"
)

// serviceCompletion 提供服务名补全：从编排文件加载已定义的服务名，供各 [name] 子命令使用。
func serviceCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	path := cfgFile
	if path == "" {
		path = config.DefaultPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		// 编排文件缺失/非法时不做补全
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(cfg.Services))
	for n := range cfg.Services {
		names = append(names, n)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	// 用自己的 completion 命令替代 cobra 默认，以便支持 install 子命令
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.AddCommand(completionCmd)
}

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell|install]",
	Short: "生成或安装命令补全脚本",
	Long:  "生成指定 shell 的补全脚本（bash/zsh/fish/powershell），或用 install 自动安装到当前用户 shell。",
	Args:  cobra.ExactArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"bash", "zsh", "fish", "powershell", "install"}, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletionV2(os.Stdout, true)
		case "zsh":
			return rootCmd.GenZshCompletion(os.Stdout)
		case "fish":
			return rootCmd.GenFishCompletion(os.Stdout, true)
		case "powershell":
			return rootCmd.GenPowerShellCompletion(os.Stdout)
		case "install":
			return installCompletion()
		default:
			return fmt.Errorf("不支持的 shell: %s（可用 bash / zsh / fish / powershell / install）", args[0])
		}
	},
}

// installCompletion 检测当前 shell 并把补全脚本写入用户配置（幂等，可重复执行）。
func installCompletion() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	switch shell := detectShell(); shell {
	case "bash":
		line := "command -v hr-compose >/dev/null && source <(hr-compose completion bash)"
		return appendLineOnce(filepath.Join(home, ".bashrc"), line, "bash")
	case "zsh":
		line := "command -v hr-compose >/dev/null && source <(hr-compose completion zsh)"
		return appendLineOnce(filepath.Join(home, ".zshrc"), line, "zsh")
	case "fish":
		path := filepath.Join(home, ".config/fish/completions/hr-compose.fish")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := rootCmd.GenFishCompletion(f, true); err != nil {
			return err
		}
		fmt.Printf("已安装 hr-compose fish 补全到 %s\n", path)
		return nil
	default:
		fmt.Println("未识别的 shell，请手动执行：hr-compose completion bash / zsh / fish")
		fmt.Println("如 bash：echo 'source <(hr-compose completion bash)' >> ~/.bashrc")
		return nil
	}
}

// detectShell 从 $SHELL 检测当前 shell；无法识别时回退到进程环境变量。
func detectShell() string {
	switch base := filepath.Base(os.Getenv("SHELL")); base {
	case "bash", "zsh", "fish":
		return base
	}
	if os.Getenv("BASH") != "" {
		return "bash"
	}
	if os.Getenv("ZSH_VERSION") != "" {
		return "zsh"
	}
	if os.Getenv("FISH_VERSION") != "" {
		return "fish"
	}
	return ""
}

// appendLineOnce 幂等地把一行配置追加到 rc 文件（已存在则跳过）。
func appendLineOnce(path, line, shell string) error {
	if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), line) {
		fmt.Printf("%s 已配置 hr-compose %s 补全，跳过\n", path, shell)
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString("\n# hr-compose 补全\n" + line + "\n"); err != nil {
		return err
	}
	fmt.Printf("已安装 hr-compose %s 补全到 %s（重启终端或 source 生效）\n", shell, path)
	return nil
}
