package buffer

import (
	"io"

	"github.com/milin2436/cloudfs-139/cloudfso/model"
)

type Block interface {
	io.ReaderAt
	io.WriterAt
	Size() int64
}

type WriteAtSeeker = model.FileWriter
type WriteAtSeekerProvider interface{ GetWriteAtSeeker() WriteAtSeeker }

type ReadAtSeeker = model.File
type ReadAtSeekerProvider interface{ GetReadAtSeeker() ReadAtSeeker }

type SizedReadAtSeeker interface {
	ReadAtSeeker
	Size() int64
}
