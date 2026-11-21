package surface

func BackgroundOf(tasks []BackgroundTask, source string) *BackgroundTasks {
	if len(tasks) == 0 {
		return nil
	}
	background := &BackgroundTasks{Tasks: tasks, Source: source}
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
