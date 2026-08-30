//go:build windows

// webdetail.go — builds the anime detail page HTML for the WebView2 layer.
// The page posts messages back via chrome.webview.postMessage:
//   {t:"status", id, v:status}   — status chip switch
//   {t:"fav", v:"type|name|id"}  — toggle studio/VA favourite
//   {t:"link", v:url}            — open external link
//   {t:"back"} / {t:"watch", id} / {t:"del", id}
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"os"
	"strconv"
	"strings"

	"butler/internal/anime"
	"butler/internal/kb"
)

// themeColors mirrors the active GDI theme so the page matches the shell.
func themeColors() (bg, side, card, card2, acc, fg, dim, red string) {
	bg = hexColor(colBg)
	side = hexColor(colSide)
	card = hexColor(colCard)
	card2 = hexColor(colCard2)
	acc = hexColor(colAcc)
	fg = hexColor(colFg)
	dim = hexColor(colDim)
	red = hexColor(colRed)
	return
}

const nlConst = "\n"

func osReadFile(p string) ([]byte, error) { return os.ReadFile(p) }

func base64Encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func hexColor(c uintptr) string {
	return fmt.Sprintf("#%06x", uint32(c)&0xFFFFFF)
}

// gdiColor converts #rrggbb back to the COLORREF layout used by Win32.
func gdiColor(hexStr string) uintptr {
	var r, g, b int
	fmt.Sscanf(hexStr, "#%02x%02x%02x", &r, &g, &b)
	return uintptr(r<<16 | g<<8 | b)
}

// coverDataURI returns the cover as a data: URI (covers are cached on disk).
func coverDataURI(recID string) string {
	path := coverDir + "\\" + recID + ".img"
	b, err := osReadFile(path)
	if err != nil || len(b) < 64 {
		return ""
	}
	mime := "image/jpeg"
	if len(b) > 8 && b[0] == 0x89 && b[1] == 'P' {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + base64Encode(b)
}

func buildDetailHTML(r *kb.Record) string {
	bg, side, card, card2, acc, fg, dim, red := themeColors()
	data := r.Data
	title, _ := data["title"].(string)
	status, _ := data["status"].(string)
	rate, _ := data["rate"].(float64)
	note, _ := data["note"].(string)
	link, _ := data["link"].(string)

	// numeric fields arrive as float64 from the JSON store
	total := numField(data, "total")
	watched := strField(data, "watched")
	air, _ := data["air_start"].(string)

	q := url.QueryEscape
	coverURI := coverDataURI(r.ID)

	statuses := []string{"想追", "在看", "看过", "搁置"}
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">")
	sb.WriteString("<style>")
	sb.WriteString("*{box-sizing:border-box;margin:0;padding:0}" + nlConst)
	sb.WriteString("body{background:" + side + ";color:" + fg + ";font:15px/1.65 'Segoe UI','Microsoft YaHei UI','Microsoft YaHei',sans-serif;-webkit-font-smoothing:antialiased}" + nlConst)
	sb.WriteString("::-webkit-scrollbar{width:10px}::-webkit-scrollbar-thumb{background:" + card2 + ";border-radius:5px}::-webkit-scrollbar-track{background:transparent}" + nlConst)
	sb.WriteString(".wrap{max-width:980px;margin:0 auto;padding:26px 30px 120px}" + nlConst)
	sb.WriteString(".hero{display:flex;gap:26px}" + nlConst)
	sb.WriteString(".cover{width:210px;height:294px;object-fit:cover;border-radius:10px;background:" + card + ";flex:none;box-shadow:0 6px 24px rgba(0,0,0,.18)}" + nlConst)
	sb.WriteString(".cover-empty{width:210px;height:294px;border-radius:10px;background:" + card + ";display:flex;align-items:center;justify-content:center;color:" + dim + ";font-size:15px;text-align:center;padding:16px;flex:none}" + nlConst)
	sb.WriteString(".hero-main{flex:1;min-width:0}" + nlConst)
	sb.WriteString("h1{font-size:30px;font-weight:700;line-height:1.3;margin-bottom:14px}" + nlConst)
	sb.WriteString(".chips{display:flex;gap:10px;flex-wrap:wrap;margin-bottom:16px}" + nlConst)
	sb.WriteString(".chip{border:1px solid " + card2 + ";background:" + card + ";color:" + fg + ";border-radius:20px;padding:5px 16px;font-size:14px;cursor:pointer;transition:all .15s}" + nlConst)
	sb.WriteString(".chip.on{background:" + acc + ";border-color:" + acc + ";color:" + bg + ";font-weight:600}" + nlConst)
	sb.WriteString(".chip:hover{border-color:" + acc + "}" + nlConst)
	sb.WriteString(".note{font-size:14.5px;line-height:1.85;color:" + fg + ";opacity:.92;margin-bottom:18px;display:-webkit-box;-webkit-line-clamp:6;-webkit-box-orient:vertical;overflow:hidden}" + nlConst)
	sb.WriteString(".meta{display:flex;gap:18px;flex-wrap:wrap;color:" + dim + ";font-size:13.5px}" + nlConst)
	sb.WriteString(".section{margin-top:30px}" + nlConst)
	sb.WriteString(".sec-title{display:flex;align-items:center;gap:10px;font-size:19px;font-weight:700;margin-bottom:14px}" + nlConst)
	sb.WriteString(".sec-title::after{content:'';flex:1;height:1px;background:" + card2 + "}" + nlConst)
	sb.WriteString(".cv-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(230px,1fr));gap:12px}" + nlConst)
	sb.WriteString(".cv{display:flex;align-items:center;gap:12px;background:" + card + ";border:1px solid " + card2 + ";border-radius:10px;padding:10px 14px;cursor:pointer;transition:border-color .15s,transform .15s}" + nlConst)
	sb.WriteString(".cv:hover{border-color:" + acc + ";transform:translateY(-1px)}" + nlConst)
	sb.WriteString(".star{font-size:17px;color:" + dim + ";flex:none}" + nlConst)
	sb.WriteString(".cv.fav .star{color:" + acc + "}" + nlConst)
	sb.WriteString(".cv-name{font-size:15px;font-weight:600}" + nlConst)
	sb.WriteString(".staff-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:10px}" + nlConst)
	sb.WriteString(".staff{background:" + card + ";border:1px solid " + card2 + ";border-radius:10px;padding:9px 14px;font-size:14px}" + nlConst)
	sb.WriteString(".staff b{color:" + acc + ";font-weight:600;margin-right:8px}" + nlConst)
	sb.WriteString(".link{display:inline-flex;align-items:center;gap:6px;color:" + acc + ";font-size:14px;text-decoration:none;border-bottom:1px dashed " + acc + ";cursor:pointer}" + nlConst)
	sb.WriteString(".bar{position:fixed;left:0;right:0;bottom:0;background:" + bg + ";border-top:1px solid " + card2 + ";padding:14px 30px;display:flex;gap:12px;z-index:9}" + nlConst)
	sb.WriteString(".bar .inner{max-width:980px;margin:0 auto;display:flex;gap:12px;width:100%}" + nlConst)
	sb.WriteString(".btn{border:0;border-radius:8px;padding:11px 26px;font-size:15px;font-weight:600;cursor:pointer;font-family:inherit;transition:opacity .15s}" + nlConst)
	sb.WriteString(".btn:active{opacity:.8}" + nlConst)
	sb.WriteString(".btn.ghost{background:" + card + ";color:" + fg + "}" + nlConst)
	sb.WriteString(".btn.acc{background:" + acc + ";color:" + bg + "}" + nlConst)
	sb.WriteString(".btn.danger{background:" + red + ";color:" + bg + ";margin-left:auto}" + nlConst)
	sb.WriteString(".loading{color:" + dim + ";font-size:14px;padding:8px 0;font-style:italic}" + nlConst)
	sb.WriteString("</style></head><body>")

	// ---- hero ----
	sb.WriteString("<div class='wrap'>")
	sb.WriteString("<div class='hero'>")
	if coverURI != "" {
		sb.WriteString("<img class='cover' src='" + coverURI + "' alt=''>")
	} else {
		sb.WriteString("<div class='cover-empty'>" + html.EscapeString(title) + "</div>")
	}
	sb.WriteString("<div class='hero-main'>")
	sb.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	sb.WriteString("<div class='chips'>")
	for _, s := range statuses {
		on := " class='chip'"
		if s == status {
			on = " class='chip on'"
		}
		sb.WriteString("<button" + on + " onclick=\"send('status','" + q(r.ID) + "','" + s + "')\">" + s + "</button>")
	}
	sb.WriteString("</div>")
	if note != "" {
		sb.WriteString("<div class='note'>" + html.EscapeString(note) + "</div>")
	}
	sb.WriteString("<div class='meta'>")
	if rate > 0 {
		sb.WriteString("<span>评分 " + fmt.Sprintf("%.1f", rate) + "</span>")
	}
	if total != "" {
		sb.WriteString("<span>集数 " + total + "</span>")
	}
	if watched != "" {
		sb.WriteString("<span>已看 " + watched + "</span>")
	}
	if air != "" {
		sb.WriteString("<span>播出 " + html.EscapeString(air) + "</span>")
	}
	sb.WriteString("</div></div></div>") // hero-main, hero

	// ---- dynamic sections: rendered by data injected at load ----
	sb.WriteString("<div id='sections'></div>")
	if link != "" {
		sb.WriteString("<div class='section'><span class='link' onclick=\"send('link','" + q(link) + "')\">🔗 " + html.EscapeString(link) + "</span></div>")
	}
	sb.WriteString("</div>") // wrap

	// ---- bottom bar ----
	sb.WriteString("<div class='bar'><div class='inner'>")
	sb.WriteString("<button class='btn ghost' onclick=\"send('back','')\">← 返回</button>")
	sb.WriteString("<button class='btn acc' onclick=\"send('watch','" + q(r.ID) + "')\">▶ 看一集 +1</button>")
	sb.WriteString("<button class='btn danger' onclick=\"send('del','" + q(r.ID) + "')\">删除</button>")
	sb.WriteString("</div></div>")

	// ---- bridge + data ----
	detailJSON, _ := json.Marshal(detailForWeb(r))
	sb.WriteString("<script>")
	sb.WriteString("function send(t,v,id){var m={t:t,v:v};if(id)m.id=id;window.chrome.webview.postMessage(JSON.stringify(m));}" + nlConst)
	sb.WriteString("window.onerror=function(e){send('err',String(e));};" + nlConst)
	sb.WriteString("var DETAIL=" + string(detailJSON) + ";" + nlConst)
	sb.WriteString(wvRenderScript(bg, card, card2, acc, fg, dim))
	sb.WriteString("</script></body></html>")
	return sb.String()
}

// wvRenderScript returns the JS that expands DETAIL into DOM.
func wvRenderScript(bg, card, card2, acc, fg, dim string) string {
	return `function el(tag,cls,txt){var e=document.createElement(tag);if(cls)e.className=cls;if(txt!==undefined)e.textContent=txt;return e;}
function favKey(type,name){return type+'|'+name;}
function render(){
  var root=document.getElementById('sections');root.innerHTML='';
  var D=DETAIL;
  // studios
  if(D.studios&&D.studios.length){
    var sec=el('div','section');sec.appendChild(el('div','sec-title','制作公司'));
    var grid=el('div','cv-grid');
    D.studios.forEach(function(s){
      var d=el('div','cv'+(s.fav?' fav':''));
      d.appendChild(el('span','star','★'));
      d.appendChild(el('span','cv-name',s.name));
      d.onclick=function(){send('fav','studio|'+s.name+'|'+s.id);};
      grid.appendChild(d);
    });
    sec.appendChild(grid);root.appendChild(sec);
  }
  // cast
  if(D.cast&&D.cast.length){
    var sec2=el('div','section');sec2.appendChild(el('div','sec-title','声优 CV'));
    var grid2=el('div','cv-grid');
    D.cast.forEach(function(c){
      if(!c.va)return;
      var d=el('div','cv'+(c.fav?' fav':''));
      d.appendChild(el('span','star','★'));
      var names=el('div');
      names.appendChild(el('div','cv-name',c.name));
      d.appendChild(names);
      var va=el('div');va.style.cssText='font-size:13px;color:'+ '' ;va.textContent=c.va;
      d.appendChild(va);
      d.onclick=function(){send('fav','cv|'+c.va+'|'+c.vaid);};
      grid2.appendChild(d);
    });
    sec2.appendChild(grid2);root.appendChild(sec2);
  }
  // staff
  if(D.staff&&D.staff.length){
    var sec3=el('div','section');sec3.appendChild(el('div','sec-title','制作人员 Staff'));
    var grid3=el('div','staff-grid');
    D.staff.forEach(function(s){
      var d=el('div','staff');
      var b=document.createElement('b');b.textContent=s.role;d.appendChild(b);
      d.appendChild(document.createTextNode(s.name));
      grid3.appendChild(d);
    });
    sec3.appendChild(grid3);root.appendChild(sec3);
  }
  if(!(D.studios&&D.studios.length)&&!(D.cast&&D.cast.length)&&!(D.staff&&D.staff.length)){
    var l=el('div','loading','资料加载中…（打开片刻后自动重试）');
    root.appendChild(l);
  }
}
render();
send('hello','');
`
}

// detailForWeb flattens the record + cached detail into the JSON the page eats.
func detailForWeb(r *kb.Record) map[string]interface{} {
	out := map[string]interface{}{"id": r.ID}
	var det *anime.Detail
	if cached, ok := r.Data["_detail"].(map[string]interface{}); ok && cached != nil {
		det = detailFromCache(cached)
	}
	if det == nil {
		return out
	}
	studios := make([]map[string]interface{}, 0, len(det.Studios))
	for _, s := range det.Studios {
		studios = append(studios, map[string]interface{}{
			"id": s.ID, "name": s.Name, "fav": favExists(s.Name),
		})
	}
	out["studios"] = studios
	cast := make([]map[string]interface{}, 0, len(det.Characters))
	for _, c := range det.Characters {
		if len(c.VAs) == 0 {
			continue
		}
		va := c.VAs[0]
		cast = append(cast, map[string]interface{}{
			"name": c.Name, "va": va.Name, "vaid": va.ID, "fav": favExists(va.Name),
		})
	}
	out["cast"] = cast
	staff := make([]map[string]interface{}, 0, len(det.Staff))
	for _, s := range det.Staff {
		staff = append(staff, map[string]interface{}{"role": s.Role, "name": s.Name})
	}
	out["staff"] = staff
	return out
}

func numField(data map[string]interface{}, key string) string {
	switch v := data[key].(type) {
	case float64:
		if v > 0 {
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	case int:
		if v > 0 {
			return strconv.Itoa(v)
		}
	case string:
		if v != "" && v != "0" {
			return v
		}
	}
	return ""
}

func strField(data map[string]interface{}, key string) string {
	v, _ := data[key].(string)
	return v
}
