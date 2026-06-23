export function css() {
  return `
:root[data-theme="dark"]{
  color-scheme:dark;
  --bg:#0a0f14;
  --paper:#101820;
  --paper-2:#16212a;
  --ink:#f5f8f7;
  --text:#d3ddd9;
  --muted:#96a8a1;
  --subtle:#72857d;
  --line:#293841;
  --line-soft:#1b2831;
  --accent:#2dd4ff;
  --green:#68f36f;
  --warn:#f6b13a;
  --soft:rgba(45,212,255,.13);
  --code:#080d12;
  --code-border:#273640;
  --shadow:0 18px 42px rgba(0,0,0,.34);
}
:root,:root[data-theme="light"]{
  color-scheme:light;
  --bg:#f7f8f4;
  --paper:#ffffff;
  --paper-2:#eef4ef;
  --ink:#121b22;
  --text:#31423b;
  --muted:#61746b;
  --subtle:#8b9a93;
  --line:#d7e1da;
  --line-soft:#edf3ee;
  --accent:#007f9d;
  --green:#158347;
  --warn:#a86600;
  --soft:rgba(0,127,157,.12);
  --code:#101820;
  --code-border:#2b3a42;
  --shadow:0 16px 34px rgba(20,32,28,.1);
}
*{box-sizing:border-box}
html{scroll-behavior:smooth;scroll-padding-top:26px}
@media(prefers-reduced-motion:reduce){html{scroll-behavior:auto}*,*::before,*::after{animation:none!important;transition:none!important}}
body{margin:0;background:var(--bg);color:var(--text);font-family:Inter,ui-sans-serif,system-ui,-apple-system,Segoe UI,sans-serif;line-height:1.65;-webkit-font-smoothing:antialiased}
a{color:var(--accent);text-decoration:none}
a:hover{text-decoration:underline;text-underline-offset:.22em}
.shell{display:grid;grid-template-columns:270px minmax(0,1fr);min-height:100vh}
.sidebar{position:sticky;top:0;height:100vh;overflow:auto;padding:26px 22px;background:color-mix(in srgb,var(--paper) 96%,var(--paper-2));border-right:1px solid var(--line);scrollbar-width:thin;scrollbar-color:var(--line) transparent}
.sidebar-head{display:flex;align-items:center;gap:10px;margin-bottom:22px}
.brand{display:flex;gap:11px;align-items:center;min-width:0;flex:1;color:var(--ink);text-decoration:none}
.brand:hover{text-decoration:none}
.mark{width:36px;height:36px;flex:0 0 36px;border-radius:8px;display:grid;place-items:center;background:var(--ink);color:var(--accent);border:1px solid color-mix(in srgb,var(--accent) 32%,var(--line));box-shadow:var(--shadow)}
.mark svg{width:24px;height:24px}.brand strong{display:block;font-size:1.08rem;line-height:1.05;color:var(--ink)}.brand small{display:block;color:var(--muted);font-size:.68rem;text-transform:uppercase;letter-spacing:.08em;margin-top:3px}
.theme-toggle{width:34px;height:34px;display:inline-grid;place-items:center;border:1px solid var(--line);border-radius:8px;background:transparent;color:var(--muted);cursor:pointer}
.theme-toggle:hover{border-color:var(--accent);color:var(--accent);background:var(--soft)}
.theme-toggle svg{width:16px;height:16px}.theme-toggle .sun{display:none}:root[data-theme="dark"] .theme-toggle .sun{display:block}:root[data-theme="dark"] .theme-toggle .moon{display:none}
.theme-float{display:none}
.search{display:block;margin:0 0 22px}.search span{display:block;margin-bottom:7px;color:var(--muted);font-size:.67rem;font-weight:750;text-transform:uppercase;letter-spacing:.09em}.search input{width:100%;height:38px;border:1px solid var(--line);border-radius:8px;background:var(--paper);color:var(--text);font:inherit;font-size:.88rem;padding:0 11px;outline:none}.search input:focus{border-color:var(--accent);box-shadow:0 0 0 3px var(--soft)}.search input::placeholder{color:var(--subtle)}
nav section{margin:0 0 19px}nav h2{margin:0 0 7px;color:var(--subtle);font-size:.67rem;text-transform:uppercase;letter-spacing:.11em}
.nav-link{display:block;border-radius:7px;padding:5px 10px;color:var(--text);font-size:.9rem;line-height:1.42}.nav-link:hover{background:var(--line-soft);color:var(--ink);text-decoration:none}.nav-link.active{background:var(--soft);color:var(--accent);font-weight:750}
.no-results{display:none;color:var(--muted);font-size:.86rem;margin-top:-4px}
main{max-width:1180px;width:100%;padding:44px clamp(20px,5vw,70px) 82px;margin:0 auto}
.hero{border-bottom:1px solid var(--line);padding:10px 0 28px;margin-bottom:28px}.eyebrow{margin:0 0 10px;color:var(--green);font-size:.72rem;text-transform:uppercase;letter-spacing:.11em;font-weight:800}
h1,h2,h3,h4{color:var(--ink);line-height:1.18;letter-spacing:0}h1{font-size:2.38rem;margin:.1em 0 .34em}h2{font-size:1.48rem;margin:2em 0 .55em}h3{font-size:1.12rem;margin:1.55em 0 .35em}h4{font-size:1rem;margin:1.35em 0 .25em}
.lede{font-size:1.12rem;max-width:68ch}.actions{display:flex;gap:10px;flex-wrap:wrap;margin-top:22px}.btn{display:inline-flex;align-items:center;gap:7px;border:1px solid var(--line);border-radius:8px;padding:10px 15px;font-weight:750;color:var(--ink);background:var(--paper)}.btn.primary{background:var(--ink);border-color:var(--ink);color:var(--bg)}.btn:hover{border-color:var(--accent);color:var(--accent);background:var(--soft);text-decoration:none}.btn.primary:hover{background:var(--accent);border-color:var(--accent);color:#fff}
.home-hero{display:grid;grid-template-columns:minmax(0,.86fr) minmax(330px,1fr);gap:34px;align-items:center;border-bottom:1px solid var(--line);padding:14px 0 34px;margin-bottom:30px}.home-hero h1{font-size:clamp(2.5rem,5vw,4.3rem);line-height:1.02;margin:0 0 18px;max-width:11ch}.home-hero .lede{font-size:1.16rem}
.hero-art{min-width:0}.hero-art img{display:block;width:100%;height:auto;border-radius:8px;border:1px solid color-mix(in srgb,var(--ink) 18%,var(--line));box-shadow:var(--shadow);background:#111820}
.feature-row{grid-column:1/-1;display:flex;gap:8px;flex-wrap:wrap;margin-top:0}.feature-pill{display:inline-flex;align-items:center;gap:7px;border:1px solid var(--line);border-radius:999px;padding:6px 11px;background:var(--paper);color:var(--text);font-size:.82rem;font-weight:650;text-decoration:none}.feature-pill:hover{border-color:var(--accent);color:var(--accent);background:var(--soft);text-decoration:none}.feature-pill svg{width:15px;height:15px;color:var(--green);flex:0 0 15px}
.doc-grid{display:grid;grid-template-columns:minmax(0,72ch) 212px;gap:46px}.doc{min-width:0;overflow-wrap:break-word}.doc h1:first-child{display:none}.doc :is(h2,h3,h4){position:relative}.doc :is(h2,h3,h4) .anchor{position:absolute;left:-1.05em;color:var(--subtle);opacity:0;text-decoration:none}.doc :is(h2,h3,h4):hover .anchor{opacity:.75}
.doc p{margin:0 0 1.08em}.doc ul,.doc ol{padding-left:1.35rem;margin:0 0 1.18em}.doc li{margin:.28em 0}.doc strong{color:var(--ink)}
.doc code{font-family:"JetBrains Mono",ui-monospace,SFMono-Regular,Menlo,monospace;background:var(--line-soft);border:1px solid var(--line);border-radius:5px;padding:.08em .35em;color:var(--accent)}.doc pre{position:relative;overflow:auto;background:var(--code);color:#e2e8f0;border-radius:8px;padding:14px 17px;margin:1.35em 0;border:1px solid var(--code-border)}.doc pre code{display:block;background:transparent;border:0;color:inherit;padding:0;font-size:.88rem;white-space:pre}.copy{position:absolute;top:8px;right:8px;border:1px solid rgba(255,255,255,.18);border-radius:6px;background:rgba(255,255,255,.07);color:#e2e8f0;font:700 .7rem/1 Inter,sans-serif;padding:4px 9px;cursor:pointer;opacity:0}.doc pre:hover .copy,.copy:focus{opacity:1}.copy.copied{background:var(--accent);border-color:var(--accent);opacity:1}
.doc table{border-collapse:collapse;width:100%;font-size:.93rem;margin:1.25em 0}.doc th,.doc td{border-bottom:1px solid var(--line);padding:8px;text-align:left;vertical-align:top}.doc th{background:var(--line-soft);color:var(--ink)}
.toc{position:sticky;top:28px;align-self:start;border-left:1px solid var(--line);padding-left:14px;font-size:.85rem;max-height:calc(100vh - 56px);overflow:auto}.toc h2{font-size:.67rem;text-transform:uppercase;letter-spacing:.1em;color:var(--subtle);margin:0 0 8px}.toc a{display:block;color:var(--muted);padding:3px 0}.toc a:hover{color:var(--accent);text-decoration:none}.toc-l3{padding-left:14px!important}
.pager{display:grid;grid-template-columns:1fr 1fr;gap:12px;border-top:1px solid var(--line);margin-top:42px;padding-top:20px}.pager a{border:1px solid var(--line);border-radius:8px;padding:11px 13px;color:var(--ink);background:var(--paper)}.pager a:hover{border-color:var(--accent);background:var(--soft);text-decoration:none}.pager small{display:block;color:var(--muted);text-transform:uppercase;font-size:.68rem;letter-spacing:.08em}
.nav-toggle{display:none;position:fixed;top:14px;right:14px;z-index:20;width:40px;height:40px;border:1px solid var(--line);border-radius:8px;background:var(--paper);color:var(--ink);box-shadow:var(--shadow);padding:9px;cursor:pointer}.nav-toggle span{display:block;height:2px;background:currentColor;border-radius:2px;margin:5px 0}
@media(max-width:960px){.shell{display:block}.sidebar{position:fixed;inset:0 28% 0 0;max-width:330px;z-index:15;transform:translateX(-102%);transition:transform .2s ease;box-shadow:var(--shadow);pointer-events:none}.sidebar.open{transform:translateX(0);pointer-events:auto}.nav-toggle{display:block}.theme-float{display:inline-grid;position:fixed;top:14px;right:62px;z-index:20;width:40px;height:40px;background:var(--paper);color:var(--ink);box-shadow:var(--shadow)}main{padding:62px 18px 56px}.home-hero{display:block}.hero-art{margin-top:24px}.doc-grid{display:block}.toc{display:none}h1{font-size:2rem}.home-hero h1{font-size:2.65rem}.doc :is(h2,h3,h4) .anchor{display:none}}
`;
}

export function js() {
  return `
const root=document.documentElement;
function readTheme(){try{return localStorage.getItem("theme")}catch{return null}}
function writeTheme(value){try{localStorage.setItem("theme",value)}catch{}}
function setTheme(value){root.dataset.theme=value;document.querySelectorAll("[data-theme-toggle]").forEach((button)=>button.setAttribute("aria-pressed",value==="dark"?"true":"false"))}
setTheme(root.dataset.theme==="dark"?"dark":"light");
document.querySelectorAll("[data-theme-toggle]").forEach((button)=>button.addEventListener("click",()=>{const next=root.dataset.theme==="dark"?"light":"dark";setTheme(next);writeTheme(next)}));
const sidebar=document.querySelector(".sidebar");
const toggle=document.querySelector(".nav-toggle");
const mobileNav=window.matchMedia("(max-width:960px)");
function syncNavA11y(open=sidebar?.classList.contains("open")){if(!sidebar)return;const hidden=mobileNav.matches&&!open;sidebar.toggleAttribute("inert",hidden);sidebar.setAttribute("aria-hidden",hidden?"true":"false")}
function setNav(open){if(!sidebar||!toggle)return;sidebar.classList.toggle("open",open);toggle.setAttribute("aria-expanded",open?"true":"false");syncNavA11y(open)}
toggle?.addEventListener("click",()=>setNav(!sidebar?.classList.contains("open")));
document.addEventListener("keydown",(event)=>{if(event.key==="Escape")setNav(false)});
document.addEventListener("click",(event)=>{if(!sidebar?.classList.contains("open"))return;if(sidebar.contains(event.target)||toggle?.contains(event.target))return;setNav(false)});
document.querySelectorAll(".nav-link").forEach((link)=>link.addEventListener("click",()=>setNav(false)));
mobileNav.addEventListener("change",()=>syncNavA11y());
syncNavA11y(false);
const search=document.getElementById("doc-search");
const empty=document.querySelector(".no-results");
search?.addEventListener("input",()=>{const query=search.value.trim().toLowerCase();let anySection=false;document.querySelectorAll(".sidebar nav section").forEach((section)=>{let anyLink=false;section.querySelectorAll(".nav-link").forEach((link)=>{const match=!query||link.textContent.toLowerCase().includes(query);link.style.display=match?"block":"none";if(match)anyLink=true});section.style.display=anyLink?"block":"none";if(anyLink)anySection=true});if(empty)empty.style.display=anySection?"none":"block"});
document.querySelectorAll(".doc pre").forEach((pre)=>{const button=document.createElement("button");button.type="button";button.className="copy";button.textContent="Copy";button.addEventListener("click",async()=>{try{await navigator.clipboard.writeText(pre.querySelector("code")?.textContent??"");button.textContent="Copied";button.classList.add("copied");setTimeout(()=>{button.textContent="Copy";button.classList.remove("copied")},1300)}catch{button.textContent="Failed";setTimeout(()=>{button.textContent="Copy"},1300)}});pre.appendChild(button)});
`;
}

export function preThemeScript() {
  return `(function(){var t;try{t=localStorage.getItem("theme")}catch(e){}document.documentElement.dataset.theme=t==="dark"?"dark":"light"})();`;
}

export function themeToggleHtml(extraClass = "") {
  const className = extraClass ? `theme-toggle ${extraClass}` : "theme-toggle";
  return `<button class="${className}" type="button" aria-label="Toggle theme" aria-pressed="true" data-theme-toggle>
    <svg class="moon" viewBox="0 0 20 20" aria-hidden="true"><path d="M14.6 12.1A6.5 6.5 0 0 1 7.4 2.7a6.5 6.5 0 1 0 7.2 9.4z" fill="currentColor"/></svg>
    <svg class="sun" viewBox="0 0 20 20" aria-hidden="true"><circle cx="10" cy="10" r="3.4" fill="currentColor"/><g stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M10 2v2M10 16v2M2 10h2M16 10h2M4.3 4.3l1.4 1.4M14.3 14.3l1.4 1.4M4.3 15.7l1.4-1.4M14.3 5.7l1.4-1.4"/></g></svg>
  </button>`;
}

export function brandMarkSvg() {
  return `<svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M5 12h14M12 5v14M5 12 3 9M19 12l2-3M12 19v3" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/><circle cx="5" cy="12" r="2.2" stroke="currentColor" stroke-width="1.8"/><circle cx="19" cy="12" r="2.2" stroke="currentColor" stroke-width="1.8"/><circle cx="12" cy="5" r="2.2" stroke="currentColor" stroke-width="1.8"/></svg>`;
}

export function featureIconSvg() {
  return `<svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M12 3l7 3v5.4c0 4.1-2.8 7.9-7 9.6-4.2-1.7-7-5.5-7-9.6V6l7-3Z" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"/><path d="M9 12l2 2 4-5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>`;
}

export function faviconSvg() {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" role="img" aria-label="CodeMesh"><rect width="64" height="64" rx="14" fill="#111820"/><path d="M19 34h26M32 18v28M19 34 12 26M45 34l7-8M32 46v8" fill="none" stroke="#2dd4ff" stroke-width="5" stroke-linecap="round"/><circle cx="19" cy="34" r="6" fill="#111820" stroke="#68f36f" stroke-width="4"/><circle cx="45" cy="34" r="6" fill="#111820" stroke="#68f36f" stroke-width="4"/><circle cx="32" cy="18" r="6" fill="#111820" stroke="#2dd4ff" stroke-width="4"/></svg>`;
}
