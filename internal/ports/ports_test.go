package ports

import (
	"context"
	"net"
	"os"
	"reflect"
	"testing"
)

func TestFree(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	busy := l.Addr().(*net.TCPAddr).Port
	if Free(busy) {
		t.Fatalf("port %d reported free while listening", busy)
	}
	got, err := FirstFree(busy, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got == busy {
		t.Fatal("FirstFree returned the busy port")
	}
	owners := Owners(context.Background(), busy)
	if len(owners) == 0 || owners[0].PID != os.Getpid() {
		t.Fatalf("Owners = %+v, want this process", owners)
	}
}

func TestParseLsof(t *testing.T) {
	out := "p49453\nchttpd\nf4\np61779\nchttpd\n"
	want := []Listener{{49453, "httpd"}, {61779, "httpd"}}
	if got := parseLsof(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseLsof = %+v, want %+v", got, want)
	}
	if got := Describe(want); got != "httpd (pid 49453), httpd (pid 61779)" {
		t.Fatalf("Describe = %q", got)
	}
}
