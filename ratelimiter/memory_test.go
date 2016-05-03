package ratelimiter

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestSetBucketFor(t *testing.T) {
	buckets := getTestBuckets("bucketOne", "bucketTwo")
	mem := NewMemory()
	if err := setTestBuckets(mem, buckets); err != nil {
		t.Errorf("SetBucketFor failed: %v", err)
	}
}

func TestGetBucketFor(t *testing.T) {
	buckets := getTestBuckets("bucketOne", "bucketTwo", "bucketThree")
	mem := NewMemory()
	if err := setTestBuckets(mem, buckets); err != nil {
		t.Errorf("SetBucketFor failed: %v", err)
	}

	for name, testBucket := range buckets {
		storedBucket, err := mem.GetBucketFor(name)
		if err != nil {
			t.Errorf("GetBucketFor failed: %v", err)
		}
		if storedBucket == nil {
			t.Error("Received a nil bucket")
		}
		if !reflect.DeepEqual(storedBucket, testBucket) {
			t.Error("Unexpected bucket")
		}
	}
}

func TestGarbageCollect(t *testing.T) {
	bucketName := "GC-test-bucket"
	mem := NewMemory()
	bucket := NewLeakyBucket(1, time.Second)

	if err := mem.SetBucketFor(bucketName, bucket); err != nil {
		t.Errorf("SetBucketFor failed: %v", err)
	}

	bucket.Pour(1)
	mem.GarbageCollect()

	bucket, err := mem.GetBucketFor(bucketName)
	if err == nil || err.Error() != "miss" {
		t.Errorf("Expected an error from GetBucketFor")
	}
	if bucket != nil {
		t.Errorf("GarbageCollect did not clear bucket for: %s", bucketName)
	}
}

func setTestBuckets(mem *Memory, buckets map[string]*LeakyBucket) error {
	for name, bucket := range buckets {
		if err := mem.SetBucketFor(name, bucket); err != nil {
			return err
		}
	}
	return nil
}

func getTestBuckets(names ...string) map[string]*LeakyBucket {
	buckets := make(map[string]*LeakyBucket, len(names))
	for _, name := range names {
		buckets[name] = NewLeakyBucket(1, time.Second)
	}
	return buckets
}
