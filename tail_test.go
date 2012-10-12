package tail

import (
	"testing"
)

// Test Config.MustExist
func TestMissingFile(t *testing.T) {
	_, err := TailFile("/no/such/file", Config{Follow: true, MustExist: true})
	if err == nil {
		t.Error("MustExist:true is violated")
	}
	_, err = TailFile("README.md", Config{Follow: true, MustExist: false})
	if err != nil {
		t.Error("MustExist:false is violated")
	}
}
