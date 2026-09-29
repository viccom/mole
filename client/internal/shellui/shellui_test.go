package shellui

import (
	"io/fs"
	"strings"
	"testing"
)

func TestAssetsExposeSharedShellIndex(t *testing.T) {
	assets := Assets()
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("ReadFile(index.html): %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected shared shell index.html to be non-empty")
	}
}

func TestShellHeaderUsesSingleNodeSwitchButtonText(t *testing.T) {
	assets := Assets()
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("ReadFile(index.html): %v", err)
	}
	html := string(data)
	if !strings.Contains(html, `id="btn-open-drawer"`) {
		t.Fatal("expected node switch trigger button in shell header")
	}
	if !strings.Contains(html, "节点切换") {
		t.Fatal(`expected shell header button text "节点切换"`)
	}
	if strings.Contains(html, "打开节点面板") {
		t.Fatal(`did not expect legacy button text "打开节点面板"`)
	}
}

func TestShellDrawerOpensFromLeftSide(t *testing.T) {
	assets := Assets()
	data, err := fs.ReadFile(assets, "style.css")
	if err != nil {
		t.Fatalf("ReadFile(style.css): %v", err)
	}
	css := string(data)
	if !strings.Contains(css, ".drawer {\n  position: fixed;\n  top: 0;\n  left: -420px;") {
		t.Fatal("expected drawer to be positioned on the left side")
	}
	if !strings.Contains(css, ".drawer.open {\n  left: 0;") {
		t.Fatal("expected drawer open state to slide in from the left side")
	}
}

func TestShellNodeFormDoesNotRenderCancelEditButton(t *testing.T) {
	assets := Assets()
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("ReadFile(index.html): %v", err)
	}
	html := string(data)
	if strings.Contains(html, `id="btn-cancel-edit"`) {
		t.Fatal("did not expect cancel-edit button in node drawer form header")
	}
	if strings.Contains(html, ">取消<") {
		t.Fatal(`did not expect standalone "取消" button text in node form header`)
	}
}
