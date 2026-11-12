package chat

import (
	"errors"
	"io/fs"
	"log"
	"os"
)

// perFleetMarker says chat/ already holds per-fleet directories, so chat/questions is a fleet's.
const perFleetMarker = ".per-fleet"

// RemoveOldLayout clears what the single chat left in chat/: its questions, withdrawn so a
// waiting `admiral chat ask` exits 4, and its skill directory. It runs once, since afterwards
// chat/questions is the directory of a fleet keyed `questions`.
func (s *Service) RemoveOldLayout() error {
	root, err := os.OpenRoot(s.Home)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Mkdir("chat", 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	chat, err := subDir(root, "chat")
	if err != nil {
		return err
	}
	defer chat.Close()
	if _, err := chat.Lstat(perFleetMarker); err == nil {
		return nil
	}
	for _, name := range []string{"questions", ".claude"} {
		if _, err := chat.Lstat(name); err != nil {
			continue
		}
		if err := chat.RemoveAll(name); err != nil {
			return err
		}
		log.Printf("admiral: removed chat/%s, left by the single chat", name)
	}
	f, err := chat.OpenFile(perFleetMarker, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}
