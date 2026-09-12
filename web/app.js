const defaultSettings = {
  font: "github",
  fontSize: 14,
  lineHeight: 22,
  theme: "system",
  wrapLines: true,
  sidebarWidth: 330,
  contextLines: 3,
  collapseViewed: true,
};

const state = {
  repository: null,
  projectID: "",
  projects: [],
  branches: [],
  commits: [],
  commitTotal: 0,
  loadingMoreCommits: false,
  commitTip: "",
  commit: null,
  comments: [],
  orphans: [],
  selectedRef: "",
  branchSource: "local",
  commitSearch: "",
  loadedRef: "",
  loadSequence: 0,
  historySequence: 0,
  pendingSave: null,
  activeSave: null,
  viewedSaveChains: new Map(),
  saveLoop: null,
  revisions: new Map(),
  bases: new Map(),
  contextLines: 3,
  dirty: false,
  saveGeneration: 0,
  viewed: new Set(),
  settings: { ...defaultSettings },
  pathRequestGeneration: 0,
};

const elements = {
  repoName: document.querySelector("#repo-name"),
  projectSelect: document.querySelector("#project-select"),
  addProject: document.querySelector("#add-project"),
  branchSource: document.querySelector("#branch-source"),
  branchSearch: document.querySelector("#branch-search"),
  branchSelect: document.querySelector("#branch-select"),
  commitSearch: document.querySelector("#commit-search"),
  commitCount: document.querySelector("#commit-count"),
  commitList: document.querySelector("#commit-list"),
  reviewPane: document.querySelector(".review-pane"),
  commitHeading: document.querySelector("#commit-heading"),
  copyReview: document.querySelector("#copy-review"),
  saveStatus: document.querySelector("#save-status"),
  statusBanner: document.querySelector("#status-banner"),
  diffRoot: document.querySelector("#diff-root"),
  toast: document.querySelector("#toast"),
  openSettings: document.querySelector("#open-settings"),
  settingsDialog: document.querySelector("#settings-dialog"),
  settingsForm: document.querySelector("#settings-form"),
  resetSettings: document.querySelector("#reset-settings"),
  projectDialog: document.querySelector("#project-dialog"),
  projectForm: document.querySelector("#project-form"),
  projectPathSuggestions: document.querySelector("#project-path-suggestions"),
};

async function api(path, options = {}) {
  if (state.projectID && (/^\/api\/(commits|comments|viewed)/).test(path)) {
    options.headers = new Headers(options.headers || {});
    if (!options.headers.has("X-DiffNote-Project")) options.headers.set("X-DiffNote-Project", state.projectID);
  }
  const response = await fetch(path, options);
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const body = await response.json();
      message = body.error || message;
    } catch (_) {
      // Keep the HTTP status when the response is not JSON.
    }
    const error = new Error(message);
    error.status = response.status;
    throw error;
  }
  if (response.status === 204) return null;
  return response.json();
}

async function initialize() {
  try {
    const [data, settings, projects] = await Promise.all([api("/api/state"), api("/api/settings"), api("/api/projects")]);
    state.settings = { ...defaultSettings, ...settings };
    applySettings();
    state.repository = data.repository;
    state.projectID = data.projectId || "";
    state.projects = projects || [];
    state.branches = data.branches || [];
    elements.repoName.textContent = state.repository?.root || "No project selected";
    renderProjects();
    if (!state.repository) {
      elements.branchSource.disabled = true;
      elements.branchSearch.disabled = true;
      elements.branchSelect.disabled = true;
      elements.commitList.replaceChildren(emptyState("No project", "Add a local Git repository to begin reviewing."));
      elements.diffRoot.replaceChildren(emptyState("Add a project", "Use the + button in the Project section."));
      return;
    }

    const current = state.branches.find((branch) => branch.current);
    state.selectedRef = current?.name || state.branches[0]?.name || "HEAD";
    state.branchSource = branchSourceFor(state.selectedRef);
    renderBranches();
    await loadCommits();
  } catch (error) {
    showFatal(error);
  }
}

function renderProjects() {
  elements.projectSelect.replaceChildren();
  if (!state.projects.length) {
    elements.projectSelect.append(option("", "No projects"));
    elements.projectSelect.disabled = true;
    return;
  }
  elements.projectSelect.disabled = false;
  for (const project of state.projects) {
    elements.projectSelect.append(option(project.path, project.name));
    if (project.selected) elements.projectSelect.value = project.path;
  }
}

function renderBranches() {
  elements.branchSource.replaceChildren(option("local", "Local"));
  if (state.branches.length === 0) {
    elements.branchSelect.replaceChildren(option("HEAD", "HEAD"));
    return;
  }

  const remotes = new Set();
  for (const branch of state.branches) {
    if (branch.remote) remotes.add(branch.remoteName);
  }
  for (const remote of [...remotes].sort()) elements.branchSource.append(option(remote, remote));
  elements.branchSource.value = state.branchSource;
  elements.branchSearch.value = "";
  renderBranchOptions();
}

function renderBranchOptions() {
  elements.branchSelect.replaceChildren();
  const query = elements.branchSearch.value.trim().toLowerCase();
  const branches = branchesForSource(state.branchSource).filter((branch) =>
    branchDisplayName(branch.name, state.branchSource).toLowerCase().includes(query));
  for (const branch of branches) {
    const display = branchDisplayName(branch.name, state.branchSource);
    elements.branchSelect.append(option(branch.name, display));
  }
  if (branches.some((branch) => branch.name === state.selectedRef)) {
    elements.branchSelect.value = state.selectedRef;
  } else {
    const placeholder = option("", branches.length ? "Select branch..." : "No matching branches");
    placeholder.disabled = true;
    placeholder.selected = true;
    elements.branchSelect.prepend(placeholder);
  }
  elements.branchSelect.disabled = branches.length === 0;
}

function branchesForSource(source) {
  return state.branches.filter((branch) => source === "local" ? !branch.remote : branch.remoteName === source);
}

function branchSourceFor(name) {
  const branch = state.branches.find((item) => item.name === name);
  return branch?.remote ? branch.remoteName : "local";
}

function branchDisplayName(name, source) {
  return source === "local" ? name : name.slice(source.length + 1);
}

function option(value, label) {
  const item = document.createElement("option");
  item.value = value;
  item.textContent = label;
  return item;
}

async function loadCommits() {
  try {
    await flushPendingSave(false);
  } catch (_) {
    return false;
  }
  const sequence = ++state.historySequence;
  const requestedRef = state.selectedRef;
  const requestedSearch = state.commitSearch;
  elements.commitList.replaceChildren(loadingMessage("Loading commits..."));
  state.commit = null;
  state.comments = [];
  state.orphans = [];
  renderCommitHeading();
  renderDiff();

  try {
    const page = await api(`/api/commits?ref=${encodeURIComponent(requestedRef)}&offset=0&search=${encodeURIComponent(requestedSearch)}`);
    if (sequence !== state.historySequence || requestedRef !== state.selectedRef || requestedSearch !== state.commitSearch) return true;
    state.commits = page.commits || [];
    state.commitTotal = page.total || 0;
    state.commitTip = page.tip || "";
    state.loadedRef = requestedRef;
    renderCommitList();
    if (state.commits.length) await selectCommit(state.commits[0].hash);
  } catch (error) {
    if (sequence !== state.historySequence || requestedRef !== state.selectedRef || requestedSearch !== state.commitSearch) return true;
    elements.commitList.replaceChildren(errorMessage(error));
    return false;
  }
  return true;
}

function renderCommitList() {
  elements.commitCount.textContent = state.commitTotal;
  elements.commitList.replaceChildren();
  for (const commit of state.commits) {
    elements.commitList.append(renderCommitCard(commit));
  }
}

function renderCommitCard(commit) {
  const button = document.createElement("button");
    button.type = "button";
    button.className = "commit-card";
    button.dataset.hash = commit.hash;
    button.title = commit.subject;

    const graph = document.createElement("span");
    graph.className = "graph";
    const content = document.createElement("span");
    const subject = document.createElement("span");
    subject.className = "commit-subject";
    subject.textContent = commit.subject;
    const meta = document.createElement("span");
    meta.className = "commit-meta";
    const hash = document.createElement("code");
    hash.textContent = commit.shortHash;
    const date = document.createElement("span");
    date.textContent = relativeDate(commit.date);
    meta.append(hash, date);
    content.append(subject, meta);
    button.append(graph, content);
    button.addEventListener("click", () => selectCommit(commit.hash));
  return button;
}

async function loadMoreCommits() {
  if (state.loadingMoreCommits || state.commits.length >= state.commitTotal) return;
  state.loadingMoreCommits = true;
  const sequence = state.historySequence;
  const requestedRef = state.selectedRef;
  const requestedSearch = state.commitSearch;
  try {
    const page = await api(`/api/commits?tip=${encodeURIComponent(state.commitTip)}&offset=${state.commits.length}&search=${encodeURIComponent(requestedSearch)}`);
    if (sequence !== state.historySequence || requestedRef !== state.selectedRef || requestedSearch !== state.commitSearch) return;
    const known = new Set(state.commits.map((commit) => commit.hash));
    for (const commit of page.commits || []) {
      if (known.has(commit.hash)) continue;
      state.commits.push(commit);
      elements.commitList.append(renderCommitCard(commit));
    }
    state.commitTotal = page.total || state.commitTotal;
    elements.commitCount.textContent = state.commitTotal;
  } catch (error) {
    showToast(`Unable to load more commits: ${error.message}`);
  } finally {
    state.loadingMoreCommits = false;
  }
}

async function selectCommit(hash) {
  try {
    await flushPendingSave(false);
  } catch (_) {
    return;
  }
  const sequence = ++state.loadSequence;
  state.contextLines = state.settings.contextLines;
  document.querySelectorAll(".commit-card").forEach((card) => {
    card.classList.toggle("selected", card.dataset.hash === hash);
  });
  elements.diffRoot.replaceChildren(loadingMessage("Loading diff..."));
  hideBanner();

  try {
    let [commit, review, viewedFiles] = await Promise.all([
      api(`/api/commits/${encodeURIComponent(hash)}?context=${state.contextLines}`),
      api(`/api/comments/${encodeURIComponent(hash)}`),
      api(`/api/viewed/${encodeURIComponent(hash)}`),
    ]);
    if (sequence !== state.loadSequence) return;
    if ((review.comments || []).some((comment) => !hasVisibleAnchor(comment, commit.files))) {
      commit = await api(`/api/commits/${encodeURIComponent(hash)}?context=100`);
      if (sequence !== state.loadSequence) return;
      state.contextLines = 100;
    }
    state.commit = commit;
    state.comments = review.comments || [];
    state.orphans = review.orphans || [];
    state.revisions.set(hash, review.revision);
    state.bases.set(hash, commentMap([...state.comments, ...state.orphans]));
    state.viewed = new Set(viewedFiles || []);
    if (review.recovered) {
      const suffix = state.orphans.length ? ` ${state.orphans.length} comment(s) could not be reattached automatically.` : "";
      showBanner(`Recovered comments from the same patch before a rebase.${suffix}`);
    }
    renderCommitHeading();
    renderDiff();
  } catch (error) {
    if (sequence === state.loadSequence) showFatal(error);
  }
}

function renderCommitHeading() {
  elements.commitHeading.replaceChildren();
  const title = document.createElement("h1");
  const detail = document.createElement("p");
  if (!state.commit) {
    title.textContent = "Select a commit";
    detail.textContent = "Choose a branch and commit from the left.";
  } else {
    title.textContent = state.commit.subject;
    detail.textContent = `${state.commit.shortHash} by ${state.commit.author} • ${formatDate(state.commit.date)}`;
  }
  elements.commitHeading.append(title, detail);
  updateCopyButton();
}

function renderDiff(focusCommentID = "") {
  elements.diffRoot.replaceChildren();
  if (!state.commit) {
    elements.diffRoot.append(emptyState("No commit selected", "Select a commit to start reviewing."));
    return;
  }
  if (!state.commit.files?.length) {
    if (state.orphans.length) elements.diffRoot.append(renderOrphans());
    elements.diffRoot.append(emptyState("No textual changes", "This commit has no diff to display."));
    return;
  }

  if (state.orphans.length) elements.diffRoot.append(renderOrphans());

  for (const file of state.commit.files) {
    elements.diffRoot.append(renderFile(file));
  }
  updateCopyButton();
  if (focusCommentID) {
    requestAnimationFrame(() => document.querySelector(`[data-comment-id="${CSS.escape(focusCommentID)}"] textarea`)?.focus());
  }
}

function renderFile(file) {
  const section = document.createElement("section");
  section.className = "file-diff";
  const commitHash = state.commit.hash;
  const projectID = state.projectID;
  const viewedKey = fileViewKey(file);
  section.classList.toggle("viewed", state.viewed.has(viewedKey));
  const header = document.createElement("header");
  header.className = "file-header";
  const status = document.createElement("span");
  status.className = "file-status";
  status.textContent = file.status;
  const name = document.createElement("span");
  name.className = "file-name";
  name.textContent = file.status === "deleted" ? file.oldPath : file.newPath;
  const viewed = document.createElement("label");
  viewed.className = "viewed-control";
  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.checked = state.viewed.has(viewedKey);
  const viewedText = document.createElement("span");
  viewedText.textContent = "Viewed";
  viewed.append(checkbox, viewedText);
  checkbox.addEventListener("change", () => {
    const desired = checkbox.checked;
    if (desired) state.viewed.add(viewedKey);
    else state.viewed.delete(viewedKey);
    section.classList.toggle("viewed", desired);
    const chainKey = `${commitHash}\u0000${viewedKey}`;
    const previous = state.viewedSaveChains.get(chainKey) || Promise.resolve();
    const save = previous.catch(() => {}).then(() => api(`/api/viewed/${encodeURIComponent(commitHash)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json", "X-DiffNote-Project": projectID },
        body: JSON.stringify({ fileKey: viewedKey, viewed: desired }),
      }));
    state.viewedSaveChains.set(chainKey, save);
    void save.catch((error) => {
      if (checkbox.checked !== desired) return;
      checkbox.checked = !desired;
      if (checkbox.checked) state.viewed.add(viewedKey);
      else state.viewed.delete(viewedKey);
      section.classList.toggle("viewed", checkbox.checked);
      showToast(`Unable to update viewed file: ${error.message}`);
    });
  });
  header.append(status, name, viewed);
  section.append(header);

  if (file.binary || !file.hunks.length) {
    const note = document.createElement("div");
    note.className = "binary-note";
    note.textContent = file.binary ? "Binary file changed" : "File metadata changed";
    section.append(note);
    return section;
  }

  const table = document.createElement("table");
  table.className = "diff-table";
  const body = document.createElement("tbody");
  for (const hunk of file.hunks) {
    const hunkRow = document.createElement("tr");
    hunkRow.className = "hunk-row";
    const cell = document.createElement("td");
    cell.colSpan = 3;
    const header = document.createElement("div");
    header.className = "hunk-header";
    const label = document.createElement("span");
    label.textContent = hunk.header;
    header.append(label);
    if (state.contextLines < 100) {
      const expand = document.createElement("button");
      expand.type = "button";
      expand.className = "expand-context";
      expand.textContent = "Expand context";
      expand.addEventListener("click", (event) => expandContext(event.currentTarget));
      header.prepend(expand);
    }
    cell.append(header);
    hunkRow.append(cell);
    body.append(hunkRow);

    for (const line of hunk.lines) {
      const anchor = lineAnchor(file, line);
      const lineComments = anchor
        ? state.comments.filter((comment) => sameAnchor(comment, anchor))
        : [];
      const row = document.createElement("tr");
      row.className = `line-row ${line.kind}${lineComments.length ? " has-comment" : ""}`;
      if (anchor) row.dataset.anchor = anchorKey(anchor);
      row.append(lineNumberCell(line.oldLine), lineNumberCell(line.newLine));
      const codeCell = document.createElement("td");
      codeCell.className = "line-code";
      const code = document.createElement("code");
      appendHighlightedCode(code, line.content || " ", file.status === "deleted" ? file.oldPath : file.newPath);
      codeCell.append(code);
      if (anchor) {
        const add = document.createElement("button");
        add.type = "button";
        add.className = "add-comment";
        add.textContent = "+";
        add.title = `Comment on ${anchor.side} line ${anchor.line}`;
        add.addEventListener("click", () => addComment(anchor));
        codeCell.append(add);
      }
      row.append(codeCell);
      body.append(row);
      for (const comment of lineComments) body.append(renderComment(comment));
    }
  }
  table.append(body);
  section.append(table);
  return section;
}

function lineNumberCell(value) {
  const cell = document.createElement("td");
  cell.className = "line-number";
  cell.textContent = value ?? "";
  return cell;
}

function fileViewKey(file) {
  return `${state.commit?.hash || ""}\u0000${file.oldPath}\u0000${file.newPath}`;
}

function anchorKey(anchor) {
  return JSON.stringify([anchor.filePath, anchor.side, anchor.line, anchor.context]);
}

async function expandContext(button) {
  if (!state.commit || state.contextLines >= 100) return;
  const anchorRow = nextAnchorRow(button.closest("tr"));
  const viewportTop = anchorRow?.getBoundingClientRect().top;
  const viewportAnchor = anchorRow?.dataset.anchor;
  const hash = state.commit.hash;
  const contextLines = Math.min(100, state.contextLines + 10);
  const sequence = ++state.loadSequence;
  showToast("Loading more context...");
  try {
    const commit = await api(`/api/commits/${encodeURIComponent(hash)}?context=${contextLines}`);
    if (sequence !== state.loadSequence || state.commit?.hash !== hash) return;
    state.commit = commit;
    state.contextLines = contextLines;
    renderDiff();
    if (viewportAnchor && viewportTop != null) {
      const restoredRow = [...elements.diffRoot.querySelectorAll(".line-row[data-anchor]")]
        .find((row) => row.dataset.anchor === viewportAnchor);
      if (restoredRow) elements.reviewPane.scrollTop += restoredRow.getBoundingClientRect().top - viewportTop;
    }
  } catch (error) {
    if (sequence === state.loadSequence) showToast(`Unable to expand: ${error.message}`);
  }
}

function nextAnchorRow(row) {
  for (let current = row?.nextElementSibling; current; current = current.nextElementSibling) {
    if (current.dataset.anchor) return current;
  }
  return null;
}

function appendHighlightedCode(element, source, path) {
  const hashComments = /\.(py|rb|sh|ya?ml|toml)$/i.test(path);
  const pattern = hashComments
    ? /(#.*$|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\b(?:async|await|break|case|catch|class|const|continue|def|default|defer|do|else|enum|export|false|finally|for|from|func|function|go|if|import|in|interface|let|map|match|new|nil|null|package|pass|private|public|range|return|select|struct|switch|throw|true|try|type|var|while|yield)\b|\b\d+(?:\.\d+)?\b)/g
    : /(\/\/.*$|\/\*.*?\*\/|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\b(?:async|await|break|case|catch|class|const|continue|def|default|defer|do|else|enum|export|false|finally|for|from|func|function|go|if|import|in|interface|let|map|match|new|nil|null|package|pass|private|public|range|return|select|struct|switch|throw|true|try|type|var|while|yield)\b|\b\d+(?:\.\d+)?\b)/g;
  let cursor = 0;
  for (const match of source.matchAll(pattern)) {
    element.append(document.createTextNode(source.slice(cursor, match.index)));
    const token = document.createElement("span");
    token.className = syntaxClass(match[0]);
    token.textContent = match[0];
    element.append(token);
    cursor = match.index + match[0].length;
  }
  element.append(document.createTextNode(source.slice(cursor)));
}

function syntaxClass(token) {
  if (token.startsWith("//") || token.startsWith("/*") || token.startsWith("#")) return "syntax-comment";
  if (token.startsWith('"') || token.startsWith("'") || token.startsWith("`")) return "syntax-string";
  if (/^\d/.test(token)) return "syntax-number";
  return "syntax-keyword";
}

function lineAnchor(file, line) {
  if (line.kind === "meta") return null;
  if (line.kind === "delete") {
    return { filePath: file.oldPath, side: "old", line: line.oldLine, context: line.content };
  }
  return { filePath: file.newPath, side: "new", line: line.newLine, context: line.content };
}

function sameAnchor(comment, anchor) {
  return comment.filePath === anchor.filePath && comment.side === anchor.side && comment.line === anchor.line && comment.context === anchor.context;
}

function hasVisibleAnchor(comment, files) {
  for (const file of files || []) {
    for (const hunk of file.hunks || []) {
      for (const line of hunk.lines || []) {
        const anchor = lineAnchor(file, line);
        if (anchor && sameAnchor(comment, anchor)) return true;
      }
    }
  }
  return false;
}

function addComment(anchor) {
  const comment = {
    id: globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`,
    commitHash: state.commit.hash,
    patchId: state.commit.patchId,
    ...anchor,
    body: "",
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  };
  state.comments.push(comment);
  renderDiff(comment.id);
}

function renderComment(comment) {
  const row = document.createElement("tr");
  row.className = "comment-row";
  row.dataset.commentId = comment.id;
  const cell = document.createElement("td");
  cell.colSpan = 3;
  cell.className = "comment-cell";
  const box = document.createElement("div");
  box.className = "comment-box";
  const textarea = document.createElement("textarea");
  textarea.value = comment.body;
  textarea.placeholder = "Leave review feedback for the coding agent...";
  textarea.setAttribute("aria-label", `Comment for ${comment.filePath} line ${comment.line}`);
  textarea.addEventListener("input", () => {
    comment.body = textarea.value;
    comment.updatedAt = new Date().toISOString();
    scheduleSave();
    updateCopyButton();
  });
  textarea.addEventListener("keydown", (event) => {
    if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
      event.preventDefault();
      saveComments(true);
    }
  });
  const actions = document.createElement("div");
  actions.className = "comment-actions";
  const hint = document.createElement("span");
  hint.textContent = `${comment.side} line ${comment.line} • Ctrl+Enter to save`;
  const remove = document.createElement("button");
  remove.type = "button";
  remove.className = "delete-comment";
  remove.textContent = "Delete";
  remove.addEventListener("click", () => {
    state.comments = state.comments.filter((item) => item.id !== comment.id);
    state.orphans = state.orphans.filter((item) => item.id !== comment.id);
    renderDiff();
    scheduleSave();
  });
  actions.append(hint, remove);
  box.append(textarea, actions);
  cell.append(box);
  row.append(cell);
  return row;
}

function renderOrphans() {
  const section = document.createElement("section");
  section.className = "orphaned-comments";
  const heading = document.createElement("h2");
  heading.textContent = "Unattached comments";
  const explanation = document.createElement("p");
  explanation.textContent = "These comments were preserved, but their original line context is missing or ambiguous.";
  section.append(heading, explanation);
  for (const comment of state.orphans) {
    const card = document.createElement("div");
    card.className = "orphan-card";
    const location = document.createElement("code");
    location.textContent = `${comment.filePath}:${comment.line} (${comment.side})`;
    const context = document.createElement("pre");
    context.textContent = comment.context;
    const body = document.createElement("textarea");
    body.value = comment.body;
    body.setAttribute("aria-label", `Unattached comment for ${comment.filePath} line ${comment.line}`);
    body.addEventListener("input", () => {
      comment.body = body.value;
      scheduleSave();
      updateCopyButton();
    });
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "delete-comment";
    remove.textContent = "Delete";
    remove.addEventListener("click", () => {
      state.orphans = state.orphans.filter((item) => item.id !== comment.id);
      renderDiff();
      scheduleSave();
    });
    card.append(location, context, body, remove);
    section.append(card);
  }
  return section;
}

function scheduleSave() {
  if (!state.commit) return;
  if (state.pendingSave?.timer) clearTimeout(state.pendingSave.timer);
  state.pendingSave = {
    hash: state.commit.hash,
    comments: reviewPayload(),
    generation: ++state.saveGeneration,
    timer: setTimeout(() => void flushPendingSave(false).catch(() => {}), 350),
  };
  state.dirty = true;
  updateSaveControls();
}

async function saveComments(showConfirmation) {
  if (!state.commit) return;
  if (state.pendingSave?.timer) clearTimeout(state.pendingSave.timer);
  state.pendingSave = { hash: state.commit.hash, comments: reviewPayload(), generation: ++state.saveGeneration, timer: null };
  await flushPendingSave(showConfirmation);
}

function reviewPayload() {
  return [...state.comments, ...state.orphans]
    .filter((comment) => comment.body.trim())
    .map((comment) => ({ ...comment, body: comment.body.trim() }));
}

async function flushPendingSave(showConfirmation) {
  if (state.pendingSave?.timer) clearTimeout(state.pendingSave.timer);
  if (!state.saveLoop) {
    state.saveLoop = drainPendingSaves().finally(() => {
      state.saveLoop = null;
    });
  }
  try {
    await state.saveLoop;
    state.dirty = Boolean(state.pendingSave);
    updateSaveControls();
    if (showConfirmation) showToast("Review saved");
  } catch (error) {
    showToast(`Save failed: ${error.message}`);
    throw error;
  }
}

async function drainPendingSaves() {
  while (state.pendingSave) {
    const pending = state.pendingSave;
    state.pendingSave = null;
    state.activeSave = pending;
    try {
      await persistReview(pending);
    } catch (error) {
      if (!state.pendingSave && pending.generation === state.saveGeneration) {
        state.pendingSave = { ...pending, timer: null };
      }
      throw error;
    } finally {
      state.activeSave = null;
    }
  }
}

async function persistReview(review) {
  let comments = review.comments;
  let revision = state.revisions.get(review.hash);
  let result;
  try {
    result = await putReview(review.hash, comments, revision);
  } catch (error) {
    if (error.status !== 409) throw error;
    const latest = await api(`/api/comments/${encodeURIComponent(review.hash)}`);
    const remoteComments = [...(latest.comments || []), ...(latest.orphans || [])];
    const base = state.bases.get(review.hash);
    comments = mergeConcurrentComments(comments, remoteComments, base);
    if (state.commit?.hash === review.hash && review.generation === state.saveGeneration) {
      applyCommentSnapshot(comments);
    }
    if (state.pendingSave?.hash === review.hash && state.pendingSave.generation > review.generation) {
      state.pendingSave.comments = mergeConcurrentComments(state.pendingSave.comments, remoteComments, base);
      if (state.commit?.hash === review.hash) applyCommentSnapshot(state.pendingSave.comments);
    }
    revision = latest.revision;
    result = await putReview(review.hash, comments, revision);
    if (state.commit?.hash === review.hash && review.generation === state.saveGeneration) {
      const refreshed = await api(`/api/comments/${encodeURIComponent(review.hash)}`);
      state.comments = refreshed.comments || [];
      state.orphans = refreshed.orphans || [];
      renderDiff();
      showBanner("Concurrent review changes were merged without discarding either version.");
    }
  }
  state.revisions.set(review.hash, result.revision);
  state.bases.set(review.hash, commentMap(comments));
}

function putReview(hash, comments, revision) {
  return api(`/api/comments/${encodeURIComponent(hash)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ comments, revision }),
  });
}

function mergeConcurrentComments(local, remote, base = new Map()) {
  const localByID = commentMap(local);
  const remoteByID = commentMap(remote);
  const ids = new Set([...base.keys(), ...localByID.keys(), ...remoteByID.keys()]);
  const merged = [];
  for (const id of ids) {
    const baseComment = base.get(id);
    const localComment = localByID.get(id);
    const remoteComment = remoteByID.get(id);
    if (!baseComment) {
      if (localComment) merged.push(localComment);
      if (remoteComment && (!localComment || commentSignature(remoteComment) !== commentSignature(localComment))) {
        merged.push(localComment ? concurrentCopy(remoteComment) : remoteComment);
      }
      continue;
    }
    if (!localComment && !remoteComment) continue;
    if (!localComment) {
      if (commentSignature(remoteComment) !== commentSignature(baseComment)) merged.push(remoteComment);
      continue;
    }
    if (!remoteComment) {
      if (commentSignature(localComment) !== commentSignature(baseComment)) merged.push(localComment);
      continue;
    }
    const localChanged = commentSignature(localComment) !== commentSignature(baseComment);
    const remoteChanged = commentSignature(remoteComment) !== commentSignature(baseComment);
    if (!localChanged && remoteChanged) merged.push(remoteComment);
    else {
      merged.push(localComment);
      if (localChanged && remoteChanged && commentSignature(localComment) !== commentSignature(remoteComment)) {
        merged.push(concurrentCopy(remoteComment));
      }
    }
  }
  return merged;
}

function concurrentCopy(comment) {
  return {
    ...comment,
    id: globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`,
    body: `Concurrent version:\n${comment.body}`,
  };
}

function applyCommentSnapshot(comments) {
  state.comments = comments.filter((comment) => hasVisibleAnchor(comment, state.commit?.files));
  state.orphans = comments.filter((comment) => !hasVisibleAnchor(comment, state.commit?.files));
  renderDiff();
}

function commentMap(comments) {
  return new Map(comments.map((comment) => [comment.id, { ...comment }]));
}

function commentSignature(comment) {
  return JSON.stringify([comment.filePath, comment.side, comment.line, comment.context, comment.body]);
}

function updateCopyButton() {
  elements.copyReview.disabled = !state.commit || ![...state.comments, ...state.orphans].some((comment) => comment.body.trim());
  updateSaveControls();
}

function updateSaveControls() {
  elements.saveStatus.textContent = state.dirty ? "Unsaved" : "Saved";
  elements.saveStatus.classList.toggle("unsaved", state.dirty);
}

async function copyReview() {
  if (!state.commit) return;
  if (![...state.comments, ...state.orphans].some((comment) => comment.body.trim())) return;
  try {
    await saveComments(false);
  } catch (_) {
    return;
  }
  const comments = state.comments.filter((comment) => comment.body.trim());
  const orphans = state.orphans.filter((comment) => comment.body.trim());

  const lines = [
    `## Review of ${state.commit.shortHash}: ${state.commit.subject}`,
    "",
  ];
  for (const comment of comments) {
    const path = comment.filePath.replaceAll("`", "\\`");
    lines.push(`- \`${path}:${comment.line}\` (${comment.side} line)`);
    if (comment.context) lines.push(`  > ${comment.context.slice(0, 240)}`);
    for (const bodyLine of comment.body.trim().split("\n")) lines.push(`  ${bodyLine}`);
    lines.push("");
  }
  if (orphans.length) {
    lines.push("### Unattached comments", "");
    for (const comment of orphans) {
      const path = comment.filePath.replaceAll("`", "\\`");
      lines.push(`- \`${path}:${comment.line}\` (${comment.side} line, outdated anchor)`);
      if (comment.context) lines.push(`  > ${comment.context.slice(0, 240)}`);
      for (const bodyLine of comment.body.trim().split("\n")) lines.push(`  ${bodyLine}`);
      lines.push("");
    }
  }
  const text = lines.join("\n").trimEnd() + "\n";
  try {
    await navigator.clipboard.writeText(text);
  } catch (_) {
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.style.position = "fixed";
    textarea.style.opacity = "0";
    document.body.append(textarea);
    textarea.select();
    document.execCommand("copy");
    textarea.remove();
  }
  const count = comments.length + orphans.length;
  showToast(`Copied ${count} comment${count === 1 ? "" : "s"}`);
}

function relativeDate(value) {
  const date = new Date(value);
  const seconds = Math.round((date.getTime() - Date.now()) / 1000);
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  const units = [
    ["year", 31_536_000],
    ["month", 2_592_000],
    ["day", 86_400],
    ["hour", 3_600],
    ["minute", 60],
  ];
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) return formatter.format(Math.round(seconds / size), unit);
  }
  return "just now";
}

function formatDate(value) {
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

function applySettings() {
  const fonts = {
    github: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
    system: "monospace",
    jetbrains: '"JetBrains Mono", "DejaVu Sans Mono", monospace',
    fira: '"Fira Code", "DejaVu Sans Mono", monospace',
  };
  const root = document.documentElement;
  if (state.settings.theme === "system") delete root.dataset.theme;
  else root.dataset.theme = state.settings.theme;
  root.style.setProperty("--code-font", fonts[state.settings.font] || fonts.github);
  root.style.setProperty("--code-size", `${state.settings.fontSize}px`);
  root.style.setProperty("--code-line-height", `${state.settings.lineHeight}px`);
  root.style.setProperty("--sidebar-width", `${state.settings.sidebarWidth}px`);
  root.classList.toggle("no-wrap", !state.settings.wrapLines);
  root.classList.toggle("collapse-viewed", state.settings.collapseViewed);
}

function fillSettingsForm(settings) {
  for (const [name, value] of Object.entries(settings)) {
    const input = elements.settingsForm.elements.namedItem(name);
    if (!input) continue;
    if (input.type === "checkbox") input.checked = value;
    else input.value = value;
  }
  updateSettingOutputs();
}

function readSettingsForm() {
  const form = new FormData(elements.settingsForm);
  return {
    font: form.get("font"),
    fontSize: Number(form.get("fontSize")),
    lineHeight: Number(form.get("lineHeight")),
    theme: form.get("theme"),
    wrapLines: form.get("wrapLines") === "on",
    sidebarWidth: Number(form.get("sidebarWidth")),
    contextLines: Number(form.get("contextLines")),
    collapseViewed: form.get("collapseViewed") === "on",
  };
}

function updateSettingOutputs() {
  for (const output of elements.settingsForm.querySelectorAll("output[data-for]")) {
    const input = elements.settingsForm.elements.namedItem(output.dataset.for);
    output.value = `${input.value}px`;
  }
}

function loadingMessage(text) {
  const element = document.createElement("div");
  element.className = "empty-state";
  element.textContent = text;
  return element;
}

function errorMessage(error) {
  return emptyState("Unable to load", error.message);
}

function emptyState(title, detail) {
  const element = document.createElement("div");
  element.className = "empty-state";
  const content = document.createElement("div");
  const heading = document.createElement("strong");
  heading.textContent = title;
  const description = document.createElement("span");
  description.textContent = detail;
  content.append(heading, description);
  element.append(content);
  return element;
}

function showBanner(message) {
  elements.statusBanner.textContent = message;
  elements.statusBanner.hidden = false;
}

function hideBanner() {
  elements.statusBanner.hidden = true;
}

function showToast(message) {
  elements.toast.textContent = message;
  elements.toast.classList.add("visible");
  clearTimeout(showToast.timer);
  showToast.timer = setTimeout(() => elements.toast.classList.remove("visible"), 2200);
}

function showFatal(error) {
  elements.diffRoot.replaceChildren(errorMessage(error));
  showToast(error.message);
}

elements.branchSource.addEventListener("change", async () => {
  const previousSource = state.branchSource;
  const previousRef = state.selectedRef;
  state.branchSource = elements.branchSource.value;
  const first = branchesForSource(state.branchSource)[0];
  if (!first) return;
  state.selectedRef = first.name;
  elements.branchSearch.value = "";
  renderBranchOptions();
  if (!await loadCommits()) {
    state.branchSource = previousSource;
    state.selectedRef = previousRef;
    renderBranches();
  }
});
elements.branchSearch.addEventListener("input", renderBranchOptions);
elements.branchSelect.addEventListener("change", async () => {
  if (!elements.branchSelect.value) return;
  const previousRef = state.loadedRef;
  state.selectedRef = elements.branchSelect.value;
  if (!await loadCommits()) {
    state.selectedRef = previousRef;
    state.branchSource = branchSourceFor(previousRef);
    renderBranches();
  }
});
elements.commitSearch.addEventListener("input", () => {
  clearTimeout(elements.commitSearch.timer);
  elements.commitSearch.timer = setTimeout(() => {
    state.commitSearch = elements.commitSearch.value.trim();
    void loadCommits();
  }, 300);
});
elements.projectSelect.addEventListener("change", async () => {
  try {
    await flushPendingSave(false);
    await api("/api/projects/select", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path: elements.projectSelect.value }),
    });
    location.reload();
  } catch (error) {
    showToast(`Unable to select project: ${error.message}`);
    renderProjects();
  }
});
elements.addProject.addEventListener("click", () => {
  elements.projectForm.reset();
  elements.projectDialog.showModal();
  const input = elements.projectForm.elements.namedItem("path");
  void loadProjectPaths("").then((result) => {
    if (!elements.projectDialog.open) return;
    if (!input.value) input.value = result.home;
    input.focus();
  });
});
elements.projectDialog.addEventListener("close", () => {
  state.pathRequestGeneration++;
  clearTimeout(loadProjectPaths.timer);
});
elements.projectForm.elements.namedItem("path").addEventListener("input", (event) => {
  clearTimeout(loadProjectPaths.timer);
  loadProjectPaths.timer = setTimeout(() => void loadProjectPaths(event.target.value), 150);
});
elements.projectForm.addEventListener("submit", async (event) => {
  if (event.submitter?.value === "cancel") return;
  event.preventDefault();
  const path = new FormData(elements.projectForm).get("path");
  try {
    await flushPendingSave(false);
    await api("/api/projects", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path }),
    });
    location.reload();
  } catch (error) {
    showToast(`Unable to add project: ${error.message}`);
  }
});
elements.copyReview.addEventListener("click", copyReview);
elements.commitList.addEventListener("scroll", () => {
  if (elements.commitList.scrollTop + elements.commitList.clientHeight >= elements.commitList.scrollHeight - 240) {
    void loadMoreCommits();
  }
});
elements.openSettings.addEventListener("click", () => {
  fillSettingsForm(state.settings);
  elements.settingsDialog.showModal();
});
elements.settingsForm.addEventListener("input", updateSettingOutputs);
elements.settingsForm.addEventListener("submit", async (event) => {
  if (event.submitter?.value === "cancel") return;
  event.preventDefault();
  const previousContext = state.settings.contextLines;
  const settings = readSettingsForm();
  try {
    await api("/api/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(settings),
    });
    state.settings = settings;
    applySettings();
    elements.settingsDialog.close();
    if (state.commit && previousContext !== settings.contextLines) await selectCommit(state.commit.hash);
    showToast("Settings saved");
  } catch (error) {
    showToast(`Unable to save settings: ${error.message}`);
  }
});
elements.resetSettings.addEventListener("click", () => fillSettingsForm(defaultSettings));
document.addEventListener("keydown", (event) => {
  if ((event.ctrlKey || event.metaKey) && event.shiftKey && event.key === "Enter") {
    event.preventDefault();
    copyReview();
  }
});
window.addEventListener("pagehide", () => {
  const pending = state.pendingSave || state.activeSave;
  if (!pending) return;
  const body = JSON.stringify({ comments: pending.comments, revision: state.revisions.get(pending.hash) });
  if (new Blob([body]).size > 60_000) return;
  fetch(`/api/comments/${encodeURIComponent(pending.hash)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json", "X-DiffNote-Project": state.projectID },
    body,
    keepalive: true,
  });
});
window.addEventListener("beforeunload", (event) => {
  if (!state.dirty) return;
  event.preventDefault();
  event.returnValue = "";
});

initialize();

async function loadProjectPaths(path) {
  const generation = ++state.pathRequestGeneration;
  try {
    const result = await api(`/api/directories?path=${encodeURIComponent(path)}`);
    if (generation !== state.pathRequestGeneration || !elements.projectDialog.open) return { home: path, directories: [] };
    elements.projectPathSuggestions.replaceChildren();
    for (const directory of result.directories || []) {
      elements.projectPathSuggestions.append(option(directory, directory));
    }
    return result;
  } catch (error) {
    if (generation !== state.pathRequestGeneration || !elements.projectDialog.open) return { home: path, directories: [] };
    showToast(`Unable to browse paths: ${error.message}`);
    return { home: path, directories: [] };
  }
}
