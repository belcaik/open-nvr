package egresswatch

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"
)

const maxTailRead = 4 << 20

// TailState conserva la posición con el mismo JSON que FileTail de Python.
type TailState struct {
	Inode  *uint64 `json:"inode"`
	Offset int64   `json:"offset"`
}

// FileTail lee solamente líneas completas añadidas a un archivo.
type FileTail struct {
	Path  string
	State *TailState
}

func NewFileTail(path string, state *TailState) *FileTail {
	return &FileTail{Path: path, State: state}
}

func fileInode(info fs.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Ino
	}
	return 0
}

// ReadLines continúa desde el desplazamiento persistido. maxBytes <= 0 usa 4 MiB.
func (t *FileTail) ReadLines(maxBytes int) ([]string, error) {
	info, err := os.Stat(t.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	inode := fileInode(info)
	if t.State.Inode == nil || *t.State.Inode != inode || info.Size() < t.State.Offset {
		t.State.Inode = &inode
		t.State.Offset = 0
	}
	if info.Size() == t.State.Offset {
		return []string{}, nil
	}
	if maxBytes <= 0 {
		maxBytes = maxTailRead
	}
	f, err := os.Open(t.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(t.State.Offset, io.SeekStart); err != nil {
		return nil, err
	}
	data := make([]byte, maxBytes)
	n, err := f.Read(data)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	data = data[:n]
	end := strings.LastIndexByte(string(data), '\n')
	if end < 0 {
		return []string{}, nil
	}
	t.State.Offset += int64(end + 1)
	text := strings.ToValidUTF8(string(data[:end]), "�")
	if text == "" {
		return []string{}, nil
	}
	return strings.Split(text, "\n"), nil
}

// TruncateIfLarger vacía un log grande únicamente cuando ya se leyó entero.
func (t *FileTail) TruncateIfLarger(maxSize int64) (bool, error) {
	if maxSize <= 0 {
		return false, nil
	}
	info, err := os.Stat(t.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	inode := fileInode(info)
	if t.State.Inode != nil && *t.State.Inode == inode && info.Size() == t.State.Offset && info.Size() > maxSize {
		if err := os.Truncate(t.Path, 0); err != nil {
			return false, err
		}
		t.State.Offset = 0
		return true, nil
	}
	return false, nil
}
