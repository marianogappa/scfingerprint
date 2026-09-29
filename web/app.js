"use strict";

let INFO = null;

const call = (fn, ...args) => {
  const out = JSON.parse(fn(...args));
  if (out.error) throw new Error(out.error);
  return out;
};

const el = (tag, attrs = {}, ...children) => {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else if (v !== false && v != null) e.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    e.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return e;
};

const RACE = { Zerg: "Z", Terran: "T", Protoss: "P" };
const raceLetter = (r) => RACE[r] || "?";
const minutes = (s) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
const oneIn = (fpr) => {
  const n = Math.round(1 / fpr);
  return n >= 1000 ? n.toLocaleString("en-US") : String(n);
};

// A ReplaySet holds one player's games: parsing, player choice per replay,
// and the exclusions that keep the evidence honest.
class ReplaySet {
  constructor(root, title, onChange) {
    this.root = root;
    this.title = title;
    this.onChange = onChange;
    this.rows = [];
    this.name = "";
    this.others = [];
    this.build();
  }

  build() {
    const fileInput = el("input", { type: "file", accept: ".rep", multiple: true, hidden: true,
      onchange: (e) => { this.addFiles([...e.target.files]); e.target.value = ""; } });
    const dirInput = el("input", { type: "file", webkitdirectory: true, hidden: true,
      onchange: (e) => { this.addFiles([...e.target.files]); e.target.value = ""; } });
    this.drop = el("div", { class: "drop" },
      el("p", {}, "Drop .rep files or a folder here"),
      el("div", { class: "buttons" },
        el("button", { class: "secondary", type: "button", onclick: () => fileInput.click() }, "Choose replays"),
        el("button", { class: "secondary", type: "button", onclick: () => dirInput.click() }, "Choose a folder")),
      fileInput, dirInput);
    for (const ev of ["dragenter", "dragover"]) {
      this.drop.addEventListener(ev, (e) => { e.preventDefault(); this.drop.classList.add("over"); });
    }
    this.drop.addEventListener("dragleave", () => this.drop.classList.remove("over"));
    this.drop.addEventListener("drop", async (e) => {
      e.preventDefault();
      this.drop.classList.remove("over");
      this.addFiles(await filesFromDrop(e.dataTransfer));
    });
    this.body = el("div");
    this.root.append(el("div", { class: "set" }, this.title ? el("h3", {}, this.title) : null, this.drop, this.body));
  }

  async addFiles(files) {
    const reps = files.filter((f) => f.name.toLowerCase().endsWith(".rep"));
    for (const f of reps) {
      const row = { file: f.name, status: "reading" };
      this.rows.push(row);
      this.render();
      await new Promise((r) => setTimeout(r, 0));
      try {
        Object.assign(row, call(window.scfParse, new Uint8Array(await f.arrayBuffer())), { status: "ok" });
      } catch (err) {
        Object.assign(row, { status: "error", reason: "can't read this replay" });
      }
    }
    this.autoPick();
    this.render();
    this.onChange();
  }

  // The most frequent eligible name is almost always the owner of the
  // folder; replays where it doesn't appear (alts, barcodes) wait for a
  // manual pick instead of being guessed.
  autoPick() {
    if (!this.name) {
      const counts = this.nameCounts();
      this.name = counts.length && counts[0][1] >= 2 ? counts[0][0] : "";
    }
    for (const r of this.rows) {
      if (r.status !== "ok" || r.choice !== undefined) continue;
      const hit = r.players.find((p) => p.name === this.name);
      r.choice = hit ? hit.id : null;
    }
  }

  nameCounts() {
    const counts = new Map();
    for (const r of this.rows) {
      if (r.status !== "ok") continue;
      for (const p of r.players) counts.set(p.name, (counts.get(p.name) || 0) + 1);
    }
    return [...counts].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  }

  setName(name) {
    this.name = name;
    for (const r of this.rows) {
      if (r.status !== "ok") continue;
      const hit = r.players.find((p) => p.name === name);
      r.choice = hit ? hit.id : null;
    }
    this.render();
    this.onChange();
  }

  // exclusion says why a row doesn't count, or "" when it does.
  exclusion(r, seen) {
    if (r.status === "reading") return "reading…";
    if (r.status === "error") return r.reason;
    if (r.humans.length !== 2) return `${r.humans.length}-player game, only 1v1 counts`;
    if (r.players.length === 0) return "game too short to read";
    if (r.choice == null) return "pick a player";
    if (seen.has(r.game_key)) return "same game as another replay";
    if (this.others.some((o) => o.has(`${r.game_key}|${r.choice}`))) return "already counted in the other set";
    return "";
  }

  evaluate() {
    const seen = new Set();
    return this.rows.map((r) => {
      const why = this.exclusion(r, seen);
      if (!why) seen.add(r.game_key);
      return why;
    });
  }

  // usedKeys identifies each counted observation as game plus player, so one
  // game may give set A its player 1 and set B its player 2.
  usedKeys() {
    const whys = this.evaluate();
    return new Set(this.rows.filter((_, i) => !whys[i]).map((r) => `${r.game_key}|${r.choice}`));
  }

  games() {
    const whys = this.evaluate();
    return this.rows.filter((_, i) => !whys[i]).map((r) => {
      const p = r.players.find((x) => x.id === r.choice);
      return { vector: p.vector, race: p.race };
    });
  }

  clear() {
    this.rows = [];
    this.name = "";
    this.render();
    this.onChange();
  }

  render() {
    this.body.replaceChildren();
    if (!this.rows.length) return;

    const counts = this.nameCounts();
    const select = el("select", { onchange: (e) => this.setName(e.target.value) },
      el("option", { value: "" }, "— choose —"),
      counts.map(([n, c]) => el("option", { value: n, selected: n === this.name }, `${n} (${c})`)));
    this.body.append(el("div", { class: "picker" },
      el("label", {}, "Player:"), select,
      el("button", { class: "link", type: "button", onclick: () => this.clear() }, "clear all")));

    const whys = this.evaluate();
    const tbody = el("tbody");
    this.rows.forEach((r, i) => {
      const why = whys[i];
      let who = "";
      if (r.status === "ok" && r.players.length) {
        who = el("select", { onchange: (e) => {
          r.choice = e.target.value === "" ? null : Number(e.target.value);
          this.render();
          this.onChange();
        } },
          el("option", { value: "" }, "skip"),
          r.players.map((p) => el("option", { value: p.id, selected: p.id === r.choice }, `${p.name} (${raceLetter(p.race)})`)));
      }
      const meta = r.status === "ok" ? `${r.map} · ${minutes(r.seconds)} · ${r.date}` : "";
      tbody.append(el("tr", { class: why ? "out" : "" },
        el("td", { class: "file", title: r.file }, r.file),
        el("td", { class: "meta" }, meta),
        el("td", {}, who),
        el("td", { class: "why" }, why || "✓")));
    });
    this.body.append(el("div", { class: "table-scroll" }, el("table", { class: "rows" }, tbody)));

    const usable = whys.filter((w) => !w).length;
    const need = INFO.min_games;
    this.body.append(el("div", { class: `count ${usable >= need ? "ok" : "short"}` },
      usable >= need ? `${usable} games count toward the analysis.` : `${usable} of ${need} games needed. Add ${need - usable} more.`));

    const races = new Set(this.games().map((g) => g.race));
    if (races.size > 1) {
      this.body.append(el("div", { class: "note" },
        "These games use more than one race. Games off the player's main race lower the score, so a real match can come out inconclusive."));
    }
  }

  usable() { return this.evaluate().filter((w) => !w).length; }
}

async function filesFromDrop(dt) {
  const entries = [...dt.items].map((i) => i.webkitGetAsEntry && i.webkitGetAsEntry()).filter(Boolean);
  if (!entries.length) return [...dt.files];
  const out = [];
  const walk = async (entry) => {
    if (entry.isFile) {
      out.push(await new Promise((res, rej) => entry.file(res, rej)));
    } else if (entry.isDirectory) {
      const reader = entry.createReader();
      let batch;
      do {
        batch = await new Promise((res, rej) => reader.readEntries(res, rej));
        for (const e of batch) await walk(e);
      } while (batch.length);
    }
  };
  for (const e of entries) await walk(e);
  return out;
}

// ---- Rendering results. Names appear only on a strong verdict: a lead
// ---- carries no name, because a named near-miss is what people repeat.

function catalogBlock(res) {
  const top = res.candidates[0];
  if (res.verdict === "strong") {
    return {
      tone: "good",
      kicker: "Strong match",
      title: ["Plays like ", el("strong", {}, top.label)],
      lines: [
        top.search_fpr <= 0.01
          ? `Based on ${res.games} games. A random player's games would score this close to someone in the catalog less than 1 in ${oneIn(Math.max(top.search_fpr, 1e-4))} times.`
          : `Based on ${res.games} games, which point to ${top.label} far more than to anyone else in the catalog.`,
        top.liquipedia ? el("span", {}, "Liquipedia: ", el("a", { href: top.liquipedia, target: "_blank", rel: "noopener" }, top.label)) : null,
      ],
      details: candidatesTable(res.candidates.slice(0, 5)),
    };
  }
  if (res.verdict === "lead") {
    return {
      tone: "warn",
      kicker: "Inconclusive",
      title: "Close to someone in the catalog, but not close enough to name",
      lines: [
        `Based on ${res.games} games. ${LEAD_REASON[res.reason] || ""}`,
        "We don't show a name for near misses because plenty of players resemble a pro. Add more games, ideally on the same race, and check again.",
      ],
    };
  }
  return {
    tone: "neutral",
    kicker: "No match",
    title: "Not one of the catalogued players",
    lines: [`Based on ${res.games} games, this player doesn't play like any of the ${INFO.catalog_size} players in the catalog.`],
  };
}

const LEAD_REASON = {
  margin: "The closest catalogued player isn't far enough ahead of the next one to call it.",
  bar: "The closest catalogued player has a common style, so naming them takes more evidence.",
};

function candidatesTable(cands) {
  return el("details", {}, el("summary", {}, "Numbers"),
    el("table", {},
      el("tr", {}, el("th", {}, "Player"), el("th", {}, "z"), el("th", {}, "Chance a stranger scores this high")),
      cands.map((c) => el("tr", {}, el("td", {}, c.label), el("td", {}, c.z.toFixed(2)),
        el("td", {}, c.search_fpr >= 1 ? "not rare" : `${(c.search_fpr * 100).toFixed(2)}%`)))));
}

function renderResult(target, block, extra = []) {
  target.replaceChildren(el("div", { class: `result ${block.tone}` },
    el("p", { class: "kicker" }, block.kicker),
    el("h2", {}, block.title),
    block.lines.filter(Boolean).map((l) => el("p", {}, l)),
    block.details || null,
    extra));
}

function renderError(target, err) {
  target.replaceChildren(el("div", { class: "result bad" }, el("p", { class: "kicker" }, "Error"), el("p", {}, err.message)));
}

function sameBlock(res) {
  if (res.verdict === "strong") {
    return {
      tone: "good",
      kicker: "Same player",
      title: "Both sets were played by the same person",
      lines: [`Based on ${res.games} games. Two different players match this closely less than 1 in ${oneIn(Math.max(res.fpr, 1e-4))} times.`],
    };
  }
  if (res.verdict === "lead") {
    return {
      tone: "warn",
      kicker: "Inconclusive",
      title: "Possibly the same person",
      lines: [`Based on ${res.games} games. The sets resemble each other, but not enough to be sure. Add more games to both sides.`],
    };
  }
  return {
    tone: "bad",
    kicker: "Different players",
    title: "These sets don't play alike",
    lines: [
      `Based on ${res.games} games, nothing links the two sets to one person.`,
      "Games on different races, or years apart, can hide a real match.",
    ],
  };
}

function sideSummary(label, res) {
  const text = res.verdict === "strong"
    ? ["plays like ", el("strong", {}, res.candidates[0].label)]
    : res.verdict === "lead" ? "inconclusive against the catalog" : "not one of the catalogued players";
  return { text: el("p", {}, `${label} (${res.games} games): `, text), strongLabel: res.verdict === "strong" ? res.candidates[0].label : null };
}

// ---- Wiring.

function setup() {
  INFO = call(window.scfInfo);
  document.querySelectorAll(".catalog-size").forEach((e) => (e.textContent = INFO.catalog_size));
  document.querySelectorAll(".min-games").forEach((e) => (e.textContent = INFO.min_games));
  document.getElementById("model-tag").textContent = INFO.model;
  document.getElementById("feature-version").textContent = INFO.feature_version;
  document.getElementById("catalog-list").textContent = [...INFO.labels].sort((a, b) => a.localeCompare(b)).join(", ");

  const runId = document.getElementById("run-identify");
  const needId = document.getElementById("need-identify");
  const resId = document.getElementById("result-identify");
  const idSet = new ReplaySet(document.getElementById("set-identify"), "", () => {
    const n = idSet.usable();
    runId.disabled = n < INFO.min_games;
    needId.textContent = n < INFO.min_games ? `Needs ${INFO.min_games} usable games.` : "";
    resId.replaceChildren();
  });
  idSet.onChange();
  runId.addEventListener("click", () => {
    try {
      renderResult(resId, catalogBlock(call(window.scfIdentify, JSON.stringify(idSet.games()))));
    } catch (err) {
      renderError(resId, err);
    }
  });

  const runSame = document.getElementById("run-same");
  const needSame = document.getElementById("need-same");
  const resSame = document.getElementById("result-same");
  const onSameChange = () => {
    if (!setA || !setB) return;
    setA.others = [setB.usedKeys()];
    setB.others = [];
    setA.render();
    setB.render();
    const ok = setA.usable() >= INFO.min_games && setB.usable() >= INFO.min_games;
    runSame.disabled = !ok;
    needSame.textContent = ok ? "" : `Each set needs ${INFO.min_games} usable games.`;
    resSame.replaceChildren();
  };
  let setA = null;
  let setB = null;
  setA = new ReplaySet(document.getElementById("set-a"), "Set A", onSameChange);
  setB = new ReplaySet(document.getElementById("set-b"), "Set B", onSameChange);
  onSameChange();
  runSame.addEventListener("click", () => {
    try {
      const res = call(window.scfSame, JSON.stringify(setA.games()), JSON.stringify(setB.games()));
      const a = sideSummary("Set A", res.a);
      const b = sideSummary("Set B", res.b);
      let lines = [a.text, b.text];
      if (res.both) {
        lines = [sideSummary("Both sets together", res.both).text, a.text, b.text];
      } else if (a.strongLabel && b.strongLabel && a.strongLabel !== b.strongLabel) {
        lines.push(el("p", {}, "The sets identify as two different catalogued players."));
      }
      renderResult(resSame, sameBlock(res),
        el("div", { class: "sub" }, el("h4", {}, "Catalog check"), lines));
    } catch (err) {
      renderError(resSame, err);
    }
  });

  document.querySelectorAll(".tab").forEach((t) => t.addEventListener("click", () => {
    document.querySelectorAll(".tab").forEach((x) => x.setAttribute("aria-selected", String(x === t)));
    document.getElementById("tab-identify").hidden = t.dataset.tab !== "identify";
    document.getElementById("tab-same").hidden = t.dataset.tab !== "same";
  }));

  document.getElementById("loading").hidden = true;
  document.getElementById("app").hidden = false;
}

window.scfReady = () => {
  try {
    setup();
  } catch (err) {
    fatal(err.message);
  }
};

function fatal(msg) {
  document.getElementById("loading").hidden = true;
  const f = document.getElementById("fatal");
  f.textContent = `Couldn't start: ${msg}`;
  f.hidden = false;
}

(async () => {
  try {
    const go = new Go();
    const res = await WebAssembly.instantiateStreaming(fetch("scfingerprint.wasm"), go.importObject);
    go.run(res.instance);
    if (window.scfLoadError) fatal(window.scfLoadError);
  } catch (err) {
    fatal(err.message);
  }
})();
