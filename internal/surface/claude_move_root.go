package surface

import "fmt"

func ResolveClaudeMoveRoot(home, choice string) (string, error) {
	path, err := ResolveClaudeConfigRoot(home, choice)
	if err != nil {
		return "", err
	}
	roots, err := DescribeClaudeConfigRoots(home)
	if err != nil {
		return "", err
	}
	if root, ok := ClaudeConfigRootFor(roots, path); ok && root.Proxy != "" {
		return "", fmt.Errorf("Claude config directory %s runs through the %s proxy, which chooses its own account; move the session to a directly signed-in directory", root.Name, root.Proxy)
	}
	return path, nil
}
