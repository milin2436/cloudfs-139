package tool

import (
	"io"
	"regexp"

	"github.com/milin2436/cloudfs-139/cloudfso/model"
	"github.com/milin2436/cloudfs-139/cloudfso/stream"
)

type MultipartExtension struct {
	PartFileFormat  *regexp.Regexp
	SecondPartIndex int
}

type Tool interface {
	AcceptedExtensions() []string
	AcceptedMultipartExtensions() map[string]MultipartExtension
	GetMeta(ss []*stream.SeekableStream, args model.ArchiveArgs) (model.ArchiveMeta, error)
	List(ss []*stream.SeekableStream, args model.ArchiveInnerArgs) ([]model.Obj, error)
	Extract(ss []*stream.SeekableStream, args model.ArchiveInnerArgs) (io.ReadCloser, int64, error)
	Decompress(ss []*stream.SeekableStream, outputPath string, args model.ArchiveInnerArgs, up model.UpdateProgress) error
}
