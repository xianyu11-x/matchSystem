// match-replay 通过模拟器 HTTP 接口提取和运行逻辑节点时间线。
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultAPI = "http://127.0.0.1:8080/api/v1"

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "match-replay:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	root := flag.NewFlagSet("match-replay", flag.ContinueOnError)
	root.SetOutput(stderr)
	api := root.String("api", defaultAPI, "模拟器 API 地址")
	root.Usage = func() {
		fmt.Fprintln(stderr, "用法: match-replay [-api 地址] extract [-logs 日志文件] [-instance 实例ID] [-output 文件]")
		fmt.Fprintln(stderr, "      match-replay [-api 地址] inject -timeline 文件 -rule 规则ID")
	}
	if err := root.Parse(args); err != nil {
		return err
	}
	remaining := root.Args()
	if len(remaining) == 0 {
		root.Usage()
		return fmt.Errorf("缺少子命令")
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	switch remaining[0] {
	case "extract":
		flags := flag.NewFlagSet("extract", flag.ContinueOnError)
		flags.SetOutput(stderr)
		url := flags.String("api", *api, "模拟器 API 地址")
		logsPath := flags.String("logs", "", "UTF-8 录制文件、宿主日志或历史 JSONL 路径；- 表示标准输入")
		instance := flags.String("instance", "", "逻辑节点实例 ID")
		outputPath := flags.String("output", "", "输出路径，省略时写入标准输出")
		if err := flags.Parse(remaining[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("extract 不接受位置参数")
		}
		request := struct {
			Logs       *string `json:"logs,omitempty"`
			InstanceID string  `json:"instanceId,omitempty"`
		}{InstanceID: *instance}
		if *logsPath != "" {
			data, err := readPath(*logsPath, stdin)
			if err != nil {
				return fmt.Errorf("读取日志: %w", err)
			}
			logs := string(data)
			request.Logs = &logs
		}
		data, err := postJSON(client, *url, "/replay/extract", request)
		if err != nil {
			return err
		}
		var result struct {
			TimelineJSON string `json:"timelineJson"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("解析提取响应: %w", err)
		}
		if *instance != "" {
			if result.TimelineJSON == "" || !json.Valid([]byte(result.TimelineJSON)) {
				return fmt.Errorf("提取响应没有有效时间线")
			}
			data = []byte(result.TimelineJSON)
		}
		return writeResult(*outputPath, data, stdout)
	case "inject":
		flags := flag.NewFlagSet("inject", flag.ContinueOnError)
		flags.SetOutput(stderr)
		url := flags.String("api", *api, "模拟器 API 地址")
		timelinePath := flags.String("timeline", "", "时间线 JSON 路径；- 表示标准输入")
		rule := flags.String("rule", "", "目标规则的十进制 ID")
		if err := flags.Parse(remaining[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *timelinePath == "" || *rule == "" {
			return fmt.Errorf("inject 需要 -timeline 和 -rule，且不接受位置参数")
		}
		ruleID, err := strconv.ParseUint(*rule, 10, 64)
		if err != nil || ruleID == 0 || strconv.FormatUint(ruleID, 10) != *rule {
			return fmt.Errorf("规则 ID 必须是非零、规范十进制 uint64")
		}
		timeline, err := readPath(*timelinePath, stdin)
		if err != nil {
			return fmt.Errorf("读取时间线: %w", err)
		}
		if !json.Valid(timeline) {
			return fmt.Errorf("时间线不是有效 JSON")
		}
		request := struct {
			TimelineJSON string `json:"timelineJson"`
			TargetRuleID string `json:"targetRuleId"`
		}{TimelineJSON: string(timeline), TargetRuleID: *rule}
		data, err := postJSON(client, *url, "/replay/run", request)
		if err != nil {
			return err
		}
		return writeResult("", data, stdout)
	default:
		root.Usage()
		return fmt.Errorf("未知子命令 %q", remaining[0])
	}
}

func readPath(path string, stdin io.Reader) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(path)
}

func writeResult(path string, data []byte, stdout io.Writer) error {
	if path != "" {
		if err := os.WriteFile(path, data, 0600); err != nil {
			return fmt.Errorf("写入输出: %w", err)
		}
		return nil
	}
	if _, err := stdout.Write(data); err != nil {
		return err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		_, err := fmt.Fprintln(stdout)
		return err
	}
	return nil
}

func postJSON(client *http.Client, base, path string, request any) ([]byte, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	response, err := client.Post(strings.TrimRight(base, "/")+path, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("请求模拟器: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("读取模拟器响应: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("模拟器返回 %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("模拟器返回了无效 JSON")
	}
	return body, nil
}
