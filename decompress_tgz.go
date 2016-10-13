package getter

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"syscall"
)

// TarGzipDecompressor is an implementation of Decompressor that can
// decompress tar.gzip files.
type TarGzipDecompressor struct{}

func (d *TarGzipDecompressor) Decompress(dst, src string, dir bool) error {
	// If we're going into a directory we should make that first
	mkdir := dst
	if !dir {
		mkdir = filepath.Dir(dst)
	}
	if err := os.MkdirAll(mkdir, 0755); err != nil {
		return err
	}

	// File first
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	// Gzip compression is second
	gzipR, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("Error opening a gzip reader for %s: %s", src, err)
	}
	defer gzipR.Close()

	// Once gzip decompressed we have a tar format
	tarR := tar.NewReader(gzipR)
	done := false
	for {
		hdr, err := tarR.Next()
		if err == io.EOF {
			if !done {
				// Empty archive
				return fmt.Errorf("empty archive: %s", src)
			}

			return nil
		}
		if err != nil {
			return err
		}

		path := dst
		if dir {
			path = filepath.Join(path, hdr.Name)
		} else if done {
			return fmt.Errorf("expected a single file, got multiple: %s", src)
		}

		// Mark that we're done so future in single file mode errors
		done = true

		mode := hdr.FileInfo().Mode()

		switch hdr.Typeflag {
		case tar.TypeDir:
			if !dir {
				return fmt.Errorf("expected a single file: %s", src)
			}

			// A directory, just make the directory and continue unarchiving...
			if err := os.MkdirAll(path, mode); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA, tar.TypeGNULongName:
			if err := decompTgzWrite(path, mode, tarR); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeGNULongLink:
			if err := os.Symlink(hdr.Linkname, path); err != nil {
				//FIXME
				//return fmt.Errorf("error creating symlink %s -> %s: %v", hdr.Name, hdr.Linkname, err)
				log.Printf("Error creating symlink for %s -> %s: %v", path, hdr.Linkname, err)
			}
		case tar.TypeLink, tar.TypeGNUSparse:
			//TODO Don't treat hardlinks and sparse files as normal files
			if err := decompTgzWrite(path, mode, tarR); err != nil {
				return err
			}
		case tar.TypeFifo:
			log.Printf("Creating FIFO:      %s", path)
			if err := syscall.Mknod(path, uint32(mode)|syscall.S_IFIFO, 0); err != nil {
				return err
			}
		case tar.TypeBlock:
			//TODO remove hack
			if err := decompTgzWrite(path, mode, tarR); err != nil {
				return err
			}
			continue

			dev := decompTgzMakeDev(hdr.Devmajor, hdr.Devminor)
			log.Printf("Creating Block Dev: %s maj:%d min%d dev:%d", path, hdr.Devmajor, hdr.Devminor, dev)
			if err := syscall.Mknod(path, uint32(mode)|syscall.S_IFBLK, dev); err != nil {
				return err
			}
		case tar.TypeChar:
			//TODO remove hack
			if err := decompTgzWrite(path, mode, tarR); err != nil {
				return err
			}
			continue

			dev := decompTgzMakeDev(hdr.Devmajor, hdr.Devminor)
			log.Printf("Creating Char Dev:  %s maj:%d min%d dev:%d", path, hdr.Devmajor, hdr.Devminor, dev)
			if err := syscall.Mknod(path, uint32(mode)|syscall.S_IFCHR, dev); err != nil {
				return err
			}
		default:
			// Ignore unknown file types
			//TODO Optional logging of skipped entries
		}
	}
}

// decompTgzWrite creates a file at the path with the mode set, and writes the
// contents of the reader until error.
func decompTgzWrite(path string, mode os.FileMode, src io.Reader) error {
	dst, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

// decompTgzMakeDev creates a Mknod dev argument from tar's major and minor values.
//
// See makedev(3) https://github.molgen.mpg.de/git-mirror/glibc/blob/master/sysdeps/unix/sysv/linux/makedev.c
func decompTgzMakeDev(major, minor int64) int {
	return int((minor & 0xff) | ((major & 0xfff) << 8) | ((minor & ^0xff) << 12) | ((major & ^0xfff) << 32))
}
