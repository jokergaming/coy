package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func GlobalPath() (string, error) {
	if dir := os.Getenv("CRUSH_GLOBAL_CONFIG"); dir != "" {
		return filepath.Abs(filepath.Join(dir, "crushrc"))
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", errors.New("XDG_CONFIG_HOME 必须为绝对路径")
		}
		return filepath.Join(dir, "crush", "crushrc"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "crush", "crushrc"), nil
}

type Store struct {
	Path      string
	original  []byte
	mode      fs.FileMode
	exists    bool
	writeTemp func(string, string, []byte, fs.FileMode) (string, error)
}

func Load(path string) (*Store, *Document, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	s := &Store{Path: abs, mode: 0600, writeTemp: writeTemp}
	data, mode, exists, err := readFile(abs)
	if err != nil {
		return nil, nil, err
	}
	s.original, s.exists = data, exists
	if exists {
		s.mode = mode
	}
	d, err := Parse(string(data))
	return s, d, err
}

func readFile(path string) ([]byte, fs.FileMode, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0600, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, errors.New("配置必须为普通文件；不覆盖符号链接或特殊文件")
	}
	b, err := os.ReadFile(path)
	return b, info.Mode().Perm(), true, err
}

func (s *Store) check() error {
	b, mode, exists, err := readFile(s.Path)
	if err != nil {
		return err
	}
	if exists != s.exists || !bytes.Equal(b, s.original) || (exists && mode != s.mode) {
		return errors.New("配置被外部修改，请退出并重新加载后再编辑")
	}
	return nil
}

type Result struct {
	Backup   string
	Preview  string
	Changed  bool
	DryRun   bool
	Created  bool
	Document *Document
}

func (s *Store) Save(d *Document, dryRun bool) (Result, error) {
	d = d.forSave()
	r := Result{Preview: d.Preview(), DryRun: dryRun}
	content, err := d.Generate()
	if err != nil {
		return r, err
	}
	if err = s.check(); err != nil {
		return r, err
	}
	r.Changed = content != string(s.original)
	if !r.Changed {
		r.Document = d
		return r, nil
	}
	if d.ReadOnly != "" {
		return r, errors.New(d.ReadOnly)
	}
	if dryRun {
		return r, nil
	}
	dir := filepath.Dir(s.Path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return r, err
	}
	// Cooperating coy writers serialize; the content check also detects external edits.
	lock, err := os.OpenFile(s.Path+".coy.lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return r, fmt.Errorf("无法获取保存锁（其他 coy 可能正在保存）: %w", err)
	}
	defer os.Remove(lock.Name())
	if err = lock.Close(); err != nil {
		return r, err
	}
	if err = s.check(); err != nil {
		return r, err
	}
	if s.exists {
		r.Backup, err = s.writeTemp(dir, "crushrc.backup-*", s.original, s.mode&0600)
		if err != nil {
			return r, fmt.Errorf("备份失败，未修改原配置: %w", err)
		}
	}
	tmp, err := s.writeTemp(dir, ".crushrc.coy-*", []byte(content), s.mode)
	if err != nil {
		return r, err
	}
	defer os.Remove(tmp)
	if err = s.check(); err != nil {
		return r, err
	}
	if err = os.Rename(tmp, s.Path); err != nil {
		return r, err
	}
	r.Created = !s.exists
	s.original, s.exists = []byte(content), true
	next, err := Parse(content)
	if err != nil {
		return r, err
	}
	r.Document = next
	return r, nil
}

func writeTemp(dir, pattern string, data []byte, mode fs.FileMode) (path string, err error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path = f.Name()
	defer func() {
		if err != nil {
			os.Remove(path)
		}
	}()
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return path, err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return path, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return path, err
	}
	err = f.Close()
	return path, err
}
