package pipeline

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"filippo.io/age"
)

// Result describes the artifact a Stream produced.
type Result struct {
	// SHA256 and Size cover the finished bytes, the ones the consumer
	// received, so a stored object can be checked without being decrypted.
	SHA256 string
	Size   int64
}

// Stream connects a producer to a consumer through gzip and, when a
// recipient is given, age encryption, computing the checksum and size of the
// result as it passes.
//
// Nothing is staged on disk: a 40 GB database must not need 40 GB of free
// space on the host to back it up.
//
// The contract that matters is what happens when produce fails part way
// through. The pipe is closed with that error, so consume sees a read
// failure rather than a clean end of stream. Without that, a dump killed
// half way would be uploaded as a complete, valid-looking artifact.
func Stream(recipient age.Recipient, produce func(io.Writer) error, consume func(io.Reader) error) (Result, error) {
	pr, pw := io.Pipe()

	hash := sha256.New()
	counter := &countingWriter{}

	var produceErr error
	done := make(chan struct{})

	go func() {
		defer close(done)

		produceErr = writeArtifact(io.MultiWriter(pw, hash, counter), recipient, produce)

		// Hand the failure to the reader. A plain Close would look like a
		// complete stream to the consumer.
		pw.CloseWithError(produceErr)
	}()

	consumeErr := consume(pr)

	// If the consumer stopped early, unblock the producer rather than
	// deadlock on a pipe nobody is reading.
	if consumeErr != nil {
		pr.CloseWithError(consumeErr)
	} else {
		pr.Close()
	}

	// The checksum and size are only complete once the producer has
	// finished; this is also what publishes its writes to this goroutine.
	<-done

	switch {
	case produceErr != nil:
		return Result{}, produceErr
	case consumeErr != nil:
		return Result{}, consumeErr
	}

	return Result{SHA256: hex.EncodeToString(hash.Sum(nil)), Size: counter.n}, nil
}

// writeArtifact builds the writer stack and runs produce through it.
//
// The stages close from the inside out: gzip's trailer has to be written
// before age's authentication tag, and both before the stream ends. A stage
// left unclosed produces an artifact that is truncated but otherwise
// plausible, which is the failure this whole design is trying to avoid.
func writeArtifact(sink io.Writer, recipient age.Recipient, produce func(io.Writer) error) (err error) {
	dst := sink

	if recipient != nil {
		encrypted, encErr := Encrypt(sink, recipient)
		if encErr != nil {
			return encErr
		}

		// Deferred so it runs after gzip has flushed, which is the order age
		// needs, and reported rather than discarded: age writes its
		// authentication tag on close, and losing that error would report
		// success for an artifact that cannot be decrypted.
		defer func() {
			if closeErr := encrypted.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("closing encryption writer: %w", closeErr)
			}
		}()

		dst = encrypted
	}

	gz := gzip.NewWriter(dst)

	if produceErr := produce(gz); produceErr != nil {
		return produceErr
	}

	if closeErr := gz.Close(); closeErr != nil {
		return fmt.Errorf("closing gzip writer: %w", closeErr)
	}

	return nil
}

// countingWriter records how many bytes passed through it.
type countingWriter struct{ n int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))

	return len(p), nil
}
