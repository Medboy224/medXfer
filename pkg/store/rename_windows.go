package store

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileRenameInformation is FILE_RENAME_INFORMATION: the new name, relative to RootDirectory.
type fileRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

// renameNoReplace renames from to to, in the same folder of root, unless a file is at to
// (ErrExists). Handle to handle, as os.Root's own Rename, never by path (STO-13): the folder is
// opened through root, the .part relative to it without following a reparse point, then one
// NtSetInformationFile renames it there, ReplaceIfExists false.
var renameNoReplace = func(root *os.Root, from, to string) error {
	dir, err := root.Open(filepath.Dir(to))
	if err != nil {
		return err
	}
	defer dir.Close()
	dh := windows.Handle(dir.Fd())

	name, err := windows.NewNTUnicodeString(filepath.Base(from))
	if err != nil {
		return err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: dh, ObjectName: name, Attributes: windows.OBJ_CASE_INSENSITIVE}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, windows.DELETE|windows.SYNCHRONIZE, &oa, &iosb, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_NON_DIRECTORY_FILE, 0, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)

	newName, err := windows.UTF16FromString(filepath.Base(to))
	if err != nil {
		return err
	}
	newName = newName[:len(newName)-1] // FileNameLength gives the length, without the NUL
	var info fileRenameInformation
	size := max(int(unsafe.Offsetof(info.FileName))+2*len(newName), int(unsafe.Sizeof(info)))
	buf := make([]byte, size)
	ri := (*fileRenameInformation)(unsafe.Pointer(&buf[0]))
	ri.RootDirectory = dh
	ri.FileNameLength = uint32(2 * len(newName))
	copy(unsafe.Slice(&ri.FileName[0], len(newName)), newName)
	err = windows.NtSetInformationFile(h, &iosb, &buf[0], uint32(len(buf)), windows.FileRenameInformation)
	if errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION) {
		return ErrExists
	}
	return err
}
