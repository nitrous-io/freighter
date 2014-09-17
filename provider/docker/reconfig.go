package docker

// helpers for Reconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	BaseAUFSDir    = "/var/lib/docker/aufs"
	maxRenameRetry = 3
)

func (p *DockerProvider) backup(id, name string) error {
	cnrBackupPath := filepath.Join(p.backupBaseDir(), name)
	exists, err := pathExists(cnrBackupPath)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("backup: path %s already exists", cnrBackupPath)
	}

	if err := os.MkdirAll(cnrBackupPath, 0700); err != nil {
		return err
	}
	src, dst := p.backupPaths(id, name)
	if err := rename(src, dst); err != nil {
		return err
	}
	return nil
}

func (p *DockerProvider) restore(name, fromID, toID string) error {
	dst, src := p.backupPaths(toID, name)
	if exists, _ := pathExists(src); !exists {
		return fmt.Errorf("restore: path %s does not exist", src)
	}
	if exists, _ := pathExists(dst); exists {
		// remove top layer of new container

		// sanity: note that freighter runs as root; make sure we're deleting
		// an expected path
		if !strings.HasPrefix(dst, "/var/lib/docker/aufs/diff/"+toID) {
			return fmt.Errorf("restore: unexpected path %s", dst)
		}
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}
	if err := rename(src, dst); err != nil {
		return err
	}
	return nil
}

// backupPaths returns (dir to backup, location to backup to)
// Note that backup paths are based on name and independent of id, this allows
// us to invert src/dst when restoring a container, given the same name
func (p *DockerProvider) backupPaths(id, name string) (src string, dst string) {
	return filepath.Join(BaseAUFSDir, "diff", id),
		filepath.Join(p.backupBaseDir(), name)
}

// backupBaseDir is the parent dir for backups of containers during reconfig
func (p *DockerProvider) backupBaseDir() string {
	return filepath.Join(BaseAUFSDir, "_freighter_backups")
}

// rename runs os.Rename maxRenameRetry times
func rename(oldpath, newpath string) error {
	var (
		err      error
		attempts int
	)
	for attempts < maxRenameRetry {
		err = os.Rename(oldpath, newpath)
		if err == nil || !strings.HasSuffix(err.Error(), "device or resource busy") {
			return err
		}
		attempts++
		time.Sleep(time.Duration(attempts*500) * time.Millisecond) // linear backoff
	}
	return fmt.Errorf("rename: %d attempts: %s", attempts, err)
}

// pathExists returns:
// * (true, nil) if path exists
// * (false, nil) if path does not exist
// * (false, err) if error happened during stat
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
