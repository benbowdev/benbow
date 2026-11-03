// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package mysql

import (
	"bytes"
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	in := "-- dump\n" +
		"CREATE DATABASE /*!32312 IF NOT EXISTS*/ `shop` /*!40100 DEFAULT CHARACTER SET utf8mb4 */;\n" +
		"USE `shop`;\n" +
		"/*!50013 DEFINER=`root`@`localhost` SQL SECURITY DEFINER */\n" +
		"CREATE DEFINER=`admin`@`%` TRIGGER t BEFORE INSERT ON x FOR EACH ROW SET NEW.a = 1;\n" +
		"INSERT INTO `x` VALUES ('USE `shop`; DEFINER is text, not a clause'," + strings.Repeat("'a',", 300000) + "'z');\n" +
		"last line without newline"
	var out bytes.Buffer
	if err := Clean(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, gone := range []string{"CREATE DATABASE", "USE `shop`;\n", "DEFINER=`root`", "DEFINER=`admin`"} {
		if strings.Contains(got, gone) {
			t.Errorf("still has %q", gone)
		}
	}
	for _, kept := range []string{"/*!50013 SQL SECURITY DEFINER */", "CREATE TRIGGER t", "DEFINER is text", "last line without newline", "'z');"} {
		if !strings.Contains(got, kept) {
			t.Errorf("lost %q", kept)
		}
	}
}

func TestOptionFile(t *testing.T) {
	got := Server{User: "root", Password: `p"w\`, Socket: "/Applications/MAMP/tmp/mysql/mysql.sock"}.optionFile()
	if !strings.Contains(got, `password="p\"w\\"`) || !strings.Contains(got, "protocol=SOCKET") {
		t.Fatalf("option file:\n%s", got)
	}
	if got := (Server{User: "root", Host: "localhost"}).optionFile(); !strings.Contains(got, `host="127.0.0.1"`) || !strings.Contains(got, "port=3306") {
		t.Fatalf("TCP option file:\n%s", got)
	}
}
