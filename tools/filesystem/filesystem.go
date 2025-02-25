package filesystem

import (
	"context"
	"errors"
	"image"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/disintegration/imaging"
	"github.com/gabriel-vasile/mimetype"
	"github.com/montajnik-2/pocketbase/tools/list"
	"gocloud.dev/blob"
	"gocloud.dev/blob/fileblob"
	"gocloud.dev/blob/s3blob"
)

type System struct {
	ctx    context.Context
	bucket *blob.Bucket
}

// NewS3 initializes an S3 filesystem instance.
//
// NB! Make sure to call `Close()` after you are done working with it.
func NewS3(
	bucketName string,
	region string,
	endpoint string,
	accessKey string,
	secretKey string,
	s3ForcePathStyle bool,
) (*System, error) {
	ctx := context.Background() // default context

	cred := credentials.NewStaticCredentials(accessKey, secretKey, "")

	sess, err := session.NewSession(&aws.Config{
		Region:           aws.String(region),
		Endpoint:         aws.String(endpoint),
		Credentials:      cred,
		S3ForcePathStyle: aws.Bool(s3ForcePathStyle),
	})
	if err != nil {
		return nil, err
	}

	bucket, err := s3blob.OpenBucket(ctx, sess, bucketName, nil)
	if err != nil {
		return nil, err
	}

	return &System{ctx: ctx, bucket: bucket}, nil
}

// NewLocal initializes a new local filesystem instance.
//
// NB! Make sure to call `Close()` after you are done working with it.
func NewLocal(dirPath string) (*System, error) {
	ctx := context.Background() // default context

	// makes sure that the directory exist
	if err := os.MkdirAll(dirPath, os.ModePerm); err != nil {
		return nil, err
	}

	bucket, err := fileblob.OpenBucket(dirPath, nil)
	if err != nil {
		return nil, err
	}

	return &System{ctx: ctx, bucket: bucket}, nil
}

// SetContext assigns the specified context to the current filesystem.
func (s *System) SetContext(ctx context.Context) {
	s.ctx = ctx
}

// Close releases any resources used for the related filesystem.
func (s *System) Close() error {
	return s.bucket.Close()
}

// Exists checks if file with fileKey path exists or not.
func (s *System) Exists(fileKey string) (bool, error) {
	return s.bucket.Exists(s.ctx, fileKey)
}

// Attributes returns the attributes for the file with fileKey path.
func (s *System) Attributes(fileKey string) (*blob.Attributes, error) {
	return s.bucket.Attributes(s.ctx, fileKey)
}

// GetFile returns a file content reader for the given fileKey.
//
// NB! Make sure to call `Close()` after you are done working with it.
func (s *System) GetFile(fileKey string) (*blob.Reader, error) {
	br, err := s.bucket.NewReader(s.ctx, fileKey, nil)
	if err != nil {
		return nil, err
	}

	return br, nil
}

// List returns a flat list with info for all files under the specified prefix.
func (s *System) List(prefix string) ([]*blob.ListObject, error) {
	files := []*blob.ListObject{}

	iter := s.bucket.List(&blob.ListOptions{
		Prefix: prefix,
	})

	for {
		obj, err := iter.Next(s.ctx)
		if err != nil {
			if err != io.EOF {
				return nil, err
			}
			break
		}
		files = append(files, obj)
	}

	return files, nil
}

// Upload writes content into the fileKey location.
func (s *System) Upload(content []byte, fileKey string) error {
	opts := &blob.WriterOptions{
		ContentType: mimetype.Detect(content).String(),
	}

	w, writerErr := s.bucket.NewWriter(s.ctx, fileKey, opts)
	if writerErr != nil {
		return writerErr
	}

	if _, err := w.Write(content); err != nil {
		w.Close()
		return err
	}

	return w.Close()
}

// UploadFile uploads the provided multipart file to the fileKey location.
func (s *System) UploadFile(file *File, fileKey string) error {
	f, err := file.Reader.Open()
	if err != nil {
		return err
	}
	defer f.Close()

	mt, err := mimetype.DetectReader(f)
	if err != nil {
		return err
	}

	// rewind
	f.Seek(0, io.SeekStart)

	originalName := file.OriginalName
	if len(originalName) > 255 {
		// keep only the first 255 chars as a very rudimentary measure
		// to prevent the metadata to grow too big in size
		originalName = originalName[:255]
	}
	opts := &blob.WriterOptions{
		ContentType: mt.String(),
		Metadata: map[string]string{
			"original-filename": originalName,
		},
	}

	w, err := s.bucket.NewWriter(s.ctx, fileKey, opts)
	if err != nil {
		return err
	}

	if _, err := w.ReadFrom(f); err != nil {
		w.Close()
		return err
	}

	return w.Close()
}

// UploadMultipart uploads the provided multipart file to the fileKey location.
func (s *System) UploadMultipart(fh *multipart.FileHeader, fileKey string) error {
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer f.Close()

	mt, err := mimetype.DetectReader(f)
	if err != nil {
		return err
	}

	// rewind
	f.Seek(0, io.SeekStart)

	originalName := fh.Filename
	if len(originalName) > 255 {
		// keep only the first 255 chars as a very rudimentary measure
		// to prevent the metadata to grow too big in size
		originalName = originalName[:255]
	}
	opts := &blob.WriterOptions{
		ContentType: mt.String(),
		Metadata: map[string]string{
			"original-filename": originalName,
		},
	}

	w, err := s.bucket.NewWriter(s.ctx, fileKey, opts)
	if err != nil {
		return err
	}

	if _, err := w.ReadFrom(f); err != nil {
		w.Close()
		return err
	}

	return w.Close()
}

// Delete deletes stored file at fileKey location.
func (s *System) Delete(fileKey string) error {
	return s.bucket.Delete(s.ctx, fileKey)
}

// DeletePrefix deletes everything starting with the specified prefix.
func (s *System) DeletePrefix(prefix string) []error {
	failed := []error{}

	if prefix == "" {
		failed = append(failed, errors.New("Prefix mustn't be empty."))
		return failed
	}

	dirsMap := map[string]struct{}{}
	dirsMap[prefix] = struct{}{}

	// delete all files with the prefix
	// ---
	iter := s.bucket.List(&blob.ListOptions{
		Prefix: prefix,
	})
	for {
		obj, err := iter.Next(s.ctx)
		if err != nil {
			if err != io.EOF {
				failed = append(failed, err)
			}
			break
		}

		if err := s.Delete(obj.Key); err != nil {
			failed = append(failed, err)
		} else {
			dirsMap[filepath.Dir(obj.Key)] = struct{}{}
		}
	}
	// ---

	// try to delete the empty remaining dir objects
	// (this operation usually is optional and there is no need to strictly check the result)
	// ---
	// fill dirs slice
	dirs := make([]string, 0, len(dirsMap))
	for d := range dirsMap {
		dirs = append(dirs, d)
	}

	// sort the child dirs first, aka. ["a/b/c", "a/b", "a"]
	sort.SliceStable(dirs, func(i, j int) bool {
		return len(strings.Split(dirs[i], "/")) > len(strings.Split(dirs[j], "/"))
	})

	// delete dirs
	for _, d := range dirs {
		if d != "" {
			s.Delete(d)
		}
	}
	// ---

	return failed
}

var inlineServeContentTypes = []string{
	// image
	"image/png", "image/jpg", "image/jpeg", "image/gif", "image/webp", "image/x-icon", "image/bmp",
	// video
	"video/webm", "video/mp4", "video/3gpp", "video/quicktime", "video/x-ms-wmv",
	// audio
	"audio/basic", "audio/aiff", "audio/mpeg", "audio/midi", "audio/mp3", "audio/wave",
	"audio/wav", "audio/x-wav", "audio/x-mpeg", "audio/x-m4a", "audio/aac",
	// document
	"application/pdf", "application/x-pdf",
}

// manualExtensionContentTypes is a map of file extensions to content types.
var manualExtensionContentTypes = map[string]string{
	".svg": "image/svg+xml", // (see https://github.com/whatwg/mimesniff/issues/7)
	".css": "text/css",      // (see https://github.com/gabriel-vasile/mimetype/pull/113)
}

// Serve serves the file at fileKey location to an HTTP response.
func (s *System) Serve(res http.ResponseWriter, req *http.Request, fileKey string, name string) error {
	br, readErr := s.bucket.NewReader(s.ctx, fileKey, nil)
	if readErr != nil {
		return readErr
	}
	defer br.Close()

	disposition := "attachment"
	realContentType := br.ContentType()
	if list.ExistInSlice(realContentType, inlineServeContentTypes) {
		disposition = "inline"
	}

	// make an exception for specific content types and force a custom
	// content type to send in the response so that it can be loaded properly
	extContentType := realContentType
	if ct, found := manualExtensionContentTypes[filepath.Ext(name)]; found && extContentType != ct {
		extContentType = ct
	}

	setHeaderIfMissing(res, "Content-Disposition", disposition+"; filename="+name)
	setHeaderIfMissing(res, "Content-Type", extContentType)
	setHeaderIfMissing(res, "Content-Security-Policy", "default-src 'none'; media-src 'self'; style-src 'unsafe-inline'; sandbox")

	// set a default cache-control header
	// (valid for 30 days but the cache is allowed to reuse the file for any requests
	// that are made in the last day while revalidating the res in the background)
	setHeaderIfMissing(res, "Cache-Control", "max-age=2592000, stale-while-revalidate=86400")

	http.ServeContent(res, req, name, br.ModTime(), br)

	return nil
}

// note: expects key to be in a canonical form (eg. "accept-encoding" should be "Accept-Encoding").
func setHeaderIfMissing(res http.ResponseWriter, key string, value string) {
	if _, ok := res.Header()[key]; !ok {
		res.Header().Set(key, value)
	}
}

var ThumbSizeRegex = regexp.MustCompile(`^(\d+)x(\d+)(t|b|f)?$`)

// CreateThumb creates a new thumb image for the file at originalKey location.
// The new thumb file is stored at thumbKey location.
//
// thumbSize is in the format:
// - 0xH  (eg. 0x100)    - resize to H height preserving the aspect ratio
// - Wx0  (eg. 300x0)    - resize to W width preserving the aspect ratio
// - WxH  (eg. 300x100)  - resize and crop to WxH viewbox (from center)
// - WxHt (eg. 300x100t) - resize and crop to WxH viewbox (from top)
// - WxHb (eg. 300x100b) - resize and crop to WxH viewbox (from bottom)
// - WxHf (eg. 300x100f) - fit inside a WxH viewbox (without cropping)
func (s *System) CreateThumb(originalKey string, thumbKey, thumbSize string) error {
	sizeParts := ThumbSizeRegex.FindStringSubmatch(thumbSize)
	if len(sizeParts) != 4 {
		return errors.New("Thumb size must be in WxH, WxHt, WxHb or WxHf format.")
	}

	width, _ := strconv.Atoi(sizeParts[1])
	height, _ := strconv.Atoi(sizeParts[2])
	resizeType := sizeParts[3]

	if width == 0 && height == 0 {
		return errors.New("Thumb width and height cannot be zero at the same time.")
	}

	// fetch the original
	r, readErr := s.bucket.NewReader(s.ctx, originalKey, nil)
	if readErr != nil {
		return readErr
	}
	defer r.Close()

	// create imaging object from the original reader
	// (note: only the first frame for animated image formats)
	img, decodeErr := imaging.Decode(r, imaging.AutoOrientation(true))
	if decodeErr != nil {
		return decodeErr
	}

	var thumbImg *image.NRGBA

	if width == 0 || height == 0 {
		// force resize preserving aspect ratio
		thumbImg = imaging.Resize(img, width, height, imaging.CatmullRom)
	} else {
		switch resizeType {
		case "f":
			// fit
			thumbImg = imaging.Fit(img, width, height, imaging.CatmullRom)
		case "t":
			// fill and crop from top
			thumbImg = imaging.Fill(img, width, height, imaging.Top, imaging.CatmullRom)
		case "b":
			// fill and crop from bottom
			thumbImg = imaging.Fill(img, width, height, imaging.Bottom, imaging.CatmullRom)
		default:
			// fill and crop from center
			thumbImg = imaging.Fill(img, width, height, imaging.Center, imaging.CatmullRom)
		}
	}

	opts := &blob.WriterOptions{
		ContentType: r.ContentType(),
	}

	// open a thumb storage writer (aka. prepare for upload)
	w, writerErr := s.bucket.NewWriter(s.ctx, thumbKey, opts)
	if writerErr != nil {
		return writerErr
	}

	// try to detect the thumb format based on the original file name
	// (fallbacks to png on error)
	format, err := imaging.FormatFromFilename(thumbKey)
	if err != nil {
		format = imaging.PNG
	}

	// thumb encode (aka. upload)
	if err := imaging.Encode(w, thumbImg, format); err != nil {
		w.Close()
		return err
	}

	// check for close errors to ensure that the thumb was really saved
	return w.Close()
}
