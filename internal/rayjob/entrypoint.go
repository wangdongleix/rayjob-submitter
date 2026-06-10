package rayjob

import "strings"

func BuildEntrypoint(script string, args []string) string {
	runner := "bash"
	if strings.HasSuffix(script, ".py") {
		runner = "python"
	}
	cmd := []string{runner, script}
	cmd = append(cmd, args...)
	for i, part := range cmd {
		cmd[i] = shellQuote(part)
	}
	body := strings.Join([]string{
		"set -euxo pipefail",
		"cd /verl",
		strings.Join(cmd, " "),
	}, "\n")
	return "bash -lc " + shellQuote(body)
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
