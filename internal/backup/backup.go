// Пакет backup: периодический zip-бэкап каталога данных демоном приложения.
// Кросс-платформенно, только стандартная библиотека.
package backup

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Runner struct {
	DataDir string
	DestDir string
	Keep    int // сколько архивов хранить, 0 — без ограничения
	Every   time.Duration
	lastSig string
}

// Signature описывает содержимое каталога: если не изменилось — бэкап пропускается.
func (r *Runner) Signature() (string, error) {
	var b strings.Builder
	// папка назначения внутри каталога данных — не включаем её в обход
	dest := r.DestDir + string(filepath.Separator)
	err := filepath.Walk(r.DataDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			if p != r.DataDir && (p == r.DestDir || strings.HasPrefix(p, dest)) {
				return filepath.SkipDir
			}
			return nil
		}
		fmt.Fprintf(&b, "%s|%d|%d\n", p, fi.Size(), fi.ModTime().UnixNano())
		return nil
	})
	return b.String(), err
}

// BackupOnce создаёт архив, если данные менялись с прошлого раза.
// Возвращает путь к архиву ("" — если пропущено).
func (r *Runner) BackupOnce(now time.Time) (string, error) {
	sig, err := r.Signature()
	if err != nil {
		return "", err
	}
	if sig == r.lastSig {
		return "", nil
	}
	if err := os.MkdirAll(r.DestDir, 0o755); err != nil {
		return "", err
	}
	name := "hello-guest-" + now.Format("20060102-150405") + ".zip"
	path := filepath.Join(r.DestDir, name)
	if err := writeZip(r.DataDir, path); err != nil {
		return "", err
	}
	r.lastSig = sig
	if err := r.rotate(); err != nil {
		return path, err
	}
	return path, nil
}

// rotate удаляет самые старые архивы, оставляя последние Keep.
func (r *Runner) rotate() error {
	if r.Keep <= 0 {
		return nil
	}
	ents, err := os.ReadDir(r.DestDir)
	if err != nil {
		return err
	}
	var zips []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".zip") {
			zips = append(zips, e.Name())
		}
	}
	sort.Strings(zips) // имена с временем сортируются хронологично
	for len(zips) > r.Keep {
		os.Remove(filepath.Join(r.DestDir, zips[0]))
		zips = zips[1:]
	}
	return nil
}

// Start поднимает горутину: первый бэкап через every, дальше по таймеру.
func (r *Runner) Start(stop <-chan struct{}, onError func(error)) {
	go func() {
		t := time.NewTicker(r.Every)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if _, err := r.BackupOnce(time.Now()); err != nil && onError != nil {
					onError(err)
				}
			case <-stop:
				return
			}
		}
	}()
}

func writeZip(srcDir, dest string) error {
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	err = filepath.Walk(srcDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		if p == dest {
			return nil
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(w, in)
		return err
	})
	if err != nil {
		zw.Close()
		os.Remove(dest)
		return err
	}
	return zw.Close()
}
