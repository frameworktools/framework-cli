package skillsync

import (
	"os"
	"path/filepath"
)

// fsDisk is the planner's view of a project folder. Each path is read once:
// the first look is what the plan judges, and apply compares a fresh look
// against it before it writes.
type fsDisk struct {
	root    string
	looks   map[string]Look
	folders map[string][]string
}

// NewDisk is the planner's view of the project at root.
func NewDisk(root string) Disk {
	return &fsDisk{root: root, looks: map[string]Look{}, folders: map[string][]string{}}
}

func (d *fsDisk) Look(p string) Look {
	if l, ok := d.looks[p]; ok {
		return l
	}
	l := lookAt(d.root, p)
	d.looks[p] = l
	return l
}

// lookAt reads one copy: absent, unreadable as a skill, or its hash.
func lookAt(root, p string) Look {
	target, err := safePath(root, p)
	if err != nil {
		return Look{Unreadable: true}
	}
	info, err := os.Stat(target)
	if notThere(err) {
		return Look{Absent: true}
	}
	if err != nil || !info.IsDir() {
		return Look{Unreadable: true}
	}
	files, err := Scan(target)
	if err != nil {
		return Look{Unreadable: true}
	}
	h, err := Hash(files)
	if err != nil {
		return Look{Unreadable: true}
	}
	return Look{Hash: h}
}

func (d *fsDisk) FolderExists(dir string) bool {
	info, err := os.Lstat(filepath.Join(d.root, filepath.FromSlash(dir)))
	return err == nil && info.IsDir()
}

func (d *fsDisk) SkillFolders(dir string) []string {
	if f, ok := d.folders[dir]; ok {
		return f
	}
	f, _ := SkillFolders(d.root, dir)
	d.folders[dir] = f
	return f
}

func (d *fsDisk) Same(a, b string) bool { return samePath(d.root, a, b) }

// checkDirs reads each skills folder once, so a folder fw can't read stops
// the command before it writes anything.
func checkDirs(root string, dirs []string) error {
	for _, dir := range dirs {
		if _, err := SkillFolders(root, dir); err != nil {
			return err
		}
	}
	return nil
}
