package surfaces

import "github.com/zm2231/agenthail/internal/surface"

func backgroundOf(tasks []surface.BackgroundTask, source string) *surface.BackgroundTasks {
	if len(tasks) == 0 {
		return nil
	}
	background := &surface.BackgroundTasks{Tasks: tasks, Source: source}
	for _, task := range tasks {
		if task.Ambient {
			continue
		}
		background.Running = true
		if !task.Since.IsZero() && (background.Since.IsZero() || task.Since.Before(background.Since)) {
			background.Since = task.Since
		}
	}
	return background
}
