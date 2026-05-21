package tray

import "os"

func envFromOs(key string) string { return os.Getenv(key) }
