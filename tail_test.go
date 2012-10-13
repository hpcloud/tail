package tail

import (
	"testing"
	"time"
	"os"
	"io/ioutil"
	_ "fmt"
)

func init() {
	err := os.RemoveAll(".test")
	if err != nil {
		panic(err)
	}
}

// Test Config.MustExist
func TestMissingFile(t *testing.T) {
	tail, err := TailFile("/no/such/file", Config{Follow: true, MustExist: true})
	if err == nil {
		t.Error("MustExist:true is violated")
		tail.Stop()
	}
	tail, err = TailFile("/no/such/file", Config{Follow: true, MustExist: false})
	if err != nil {
		t.Error("MustExist:false is violated")
	}
	tail.Stop()
	_, err = TailFile("README.md", Config{Follow: true, MustExist: true})
	if err != nil {
		t.Error("MustExist:true on an existing file is violated")
	}
	tail.Stop()
}

func TestLocationMinusOne(t *testing.T) {
	fix := NewFixture("simple")
	err := ioutil.WriteFile(fix.path + "/test.txt", []byte("hello\nworld\n"), 0777)
	if err != nil {
		t.Error(err)
	}
	tail, err := TailFile(fix.path + "/test.txt", Config{Follow: true, Location: -1})
	if err != nil {
		t.Error(err)
	}
	go CompareTailWithLines(t, tail, []string{"hello", "world"})

	// delete after 1 second, to give tail sufficient time to read all lines.
	<-time.After(1 * time.Second)
	err = os.Remove(fix.path + "/test.txt")
	if err != nil {
		t.Error(err)
	}
	tail.Stop()
}

type Fixture struct {
	Name string
	path string
}

func NewFixture(name string) Fixture {
	fix := Fixture{name, ".test/" + name}
	os.MkdirAll(fix.path, os.ModeTemporary | 0700)
	return fix
}

func CompareTailWithLines(t *testing.T, tail *Tail, lines []string) {
	for _, line := range lines {
		tailedLine, ok := <-tail.Lines
		if !ok {
			t.Error("insufficient lines from tail")
			return
		}
		if tailedLine == nil {
			t.Errorf("tail.Lines returned nil; not possible")
			return
		}
		if tailedLine.Text != line {
			t.Errorf("mismatch; %s != %s", tailedLine.Text, line)
			return
		}
	}
	line, ok := <-tail.Lines
	if ok {
		t.Errorf("more content from tail: %s", line.Text)
	}
}
