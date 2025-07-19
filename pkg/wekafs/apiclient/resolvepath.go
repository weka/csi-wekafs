package apiclient

import (
	"context"
	"fmt"
	"github.com/google/go-querystring/query"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"k8s.io/helm/pkg/urlutil"
	"strconv"
	"strings"
)

type FilesystemResolvePath struct {
	Uid     uuid.UUID `json:"-" url:"-"`
	InodeId string    `json:"inode_id,omitempty" url:"-"`
	Path    string    `json:"-" url:"path,omitempty"`
}

func (fsr *FilesystemResolvePath) SupportsPagination() bool {
	return false
}

func (fsr *FilesystemResolvePath) CombinePartialResponse(next ApiObjectResponse) error {
	//TODO implement me
	panic("implement me")
}

func (fsr *FilesystemResolvePath) GetType() string {
	return "resolvePath"
}

func (fsr *FilesystemResolvePath) GetBasePath(a *ApiClient) string {
	fsUrl := (&FileSystem{Uid: fsr.Uid}).GetApiUrl(a)
	url, err := urlutil.URLJoin(fsUrl, fsr.GetType())
	if err != nil {
		return ""
	}
	return url
}

func (fsr *FilesystemResolvePath) GetApiUrl(a *ApiClient) string {
	return fsr.GetBasePath(a)
}

func (fsr *FilesystemResolvePath) EQ(q ApiObject) bool {
	return ObjectsAreEqual(q, fsr)
}

func (fsr *FilesystemResolvePath) getImmutableFields() []string {
	return []string{"InodeId"}
}

func (fsr *FilesystemResolvePath) String() string {
	return fmt.Sprintln("FilesystemResolvePath(inodeId:", fsr.InodeId, ")")
}

func (a *ApiClient) ResolvePathToInode(ctx context.Context, fs *FileSystem, path string) (uint64, error) {
	op := "ResolvePathToInode"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	if fs == nil || path == "" {
		return 0, RequestMissingParams
	}
	p := &FilesystemResolvePath{
		Uid:  fs.Uid,
		Path: path,
	}
	q, _ := query.Values(p)

	err := a.Get(ctx, p.GetApiUrl(a), q, p)
	if err != nil {
		switch t := err.(type) {
		case *ApiNotFoundError:
			return 0, ObjectNotFoundError
		case *ApiBadRequestError:
			if strings.Contains(t.ApiResponse.Message, "PATH_NOT_FOUND") {
				return 0, ObjectNotFoundError
			}

		default:
			return 0, err
		}
	}

	if p.InodeId == "" {
		return 0, ObjectNotFoundError
	}
	return strconv.ParseUint(p.InodeId, 10, 64)
}
