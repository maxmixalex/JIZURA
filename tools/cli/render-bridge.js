// Native CLI bridge. Uses JIZURA's *existing* planning and Canvas renderer.
// No web server, audio encoder, ZIP accumulation, or browser download needed.
async function __jizuraInit(opts) {
  if (!globalThis.J || !J.plan || !J.Renderer) throw new Error('JIZURA renderer is not initialized');
  const defaults = J.defaultProject();
  const project = Object.assign({}, defaults, opts.project || {});
  project.fx = Object.assign({}, defaults.fx, (opts.project || {}).fx || {});
  project.timing = Object.assign({}, defaults.timing, (opts.project || {}).timing || {});
  if (opts.lyrics !== null) project.lyrics = opts.lyrics;
  if (opts.style !== null) project.style = opts.style;
  if (opts.seed !== null) project.seed = opts.seed;
  if (opts.title !== null) project.title = opts.title;
  if (opts.lang !== null) project.lang = opts.lang;
  if (opts.aspect !== null) project.aspect = opts.aspect;
  if (opts.fps !== null) project.fps = opts.fps;
  if (opts.res !== null) project.res = opts.res;
  if (opts.noHud) project.fx.hud = 'off';
  if (!opts.project && opts.startAtZero) {
    project.timing.offset = 0;
    project.timing.tail = 0.1;
    project.timing.snap = false;
    project.timing.lineScale = 0.85;
  }
  if (!project.lyrics || !String(project.lyrics).trim()) throw new Error('Lyrics are empty');
  // The native frontend accepts directly supplied font files or installed OS font families.
  // It registers new faces under JIZURA's user-font catalogue and assigns the same
  // three roles used by the browser editor, without modifying upstream definitions.
  if (Array.isArray(project.userFonts)) for (const uf of project.userFonts) {
    if (uf && uf.key && uf.family && !J.FONTS[uf.key]) J.addUserFont(uf.key, uf.label || uf.family, uf.family, uf.weight || 400);
  }
  project.fonts = Object.assign({}, project.fonts || {});
  for (const [role, spec] of Object.entries(opts.fontFiles || {})) {
    if (!['display', 'serif', 'body'].includes(role)) throw new Error(`Unknown font role ${role}`);
    const key = `user_cli_${role}`;
    let family = spec.name;
    if (spec.base64) {
      family = `JIZURA_CLI_${role}`;
      const raw = atob(spec.base64), bytes = new Uint8Array(raw.length);
      for (let k = 0; k < raw.length; k++) bytes[k] = raw.charCodeAt(k);
      const face = new FontFace(family, bytes.buffer);
      await face.load(); document.fonts.add(face);
    }
    if (!family) throw new Error(`Font for role ${role} is empty`);
    const label = spec.filename || family;
    J.addUserFont(key, label, family, 400);
    if (spec.base64) J.FONTS[key].loaded = true;
    project.fonts[role] = key;
    project.userFonts = (project.userFonts || []).filter(x => x.key !== key)
      .concat([{ key, label, family, weight:400, source: spec.base64 ? 'file' : 'family' }]);
  }
  let plan = J.plan(project, null);
  // Per-cut manual technique choices use the EXACT same project.overrides[*].cutTech
  // format as the browser's "手法" picker; the first plan tells us how many cuts
  // exist, and a second plan applies the selections deterministically.
  if (opts.techniques && Object.keys(opts.techniques).length) {
    for (const [group, key] of Object.entries(opts.techniques)) {
      const def = J.registry(group) && J.registry(group)[key];
      if (key !== 'none' || !['decor', 'trans', 'fx'].includes(group)) {
        if (!def || def.special) throw new Error(`Unknown hand technique ${group}=${key}; see --list-techniques`);
      }
    }
    const {fx:forcedFx, ...cutTechniques} = opts.techniques;
    if (forcedFx) project.cliForcedFX = forcedFx;
    if (Object.keys(cutTechniques).length) {
      project.overrides = Object.assign({}, project.overrides || {});
      const indexes = new Map();
      for (const cut of plan.cuts) {
        if (cut.line < 0 || !J.LAYOUTS[cut.layout] || J.LAYOUTS[cut.layout].special) continue;
        const k = indexes.get(cut.line) || 0; indexes.set(cut.line, k + 1);
        const li = cut.line;
        const ov = Object.assign({}, project.overrides[li] || {});
        const slots = Object.assign({}, ov.cutTech || {});
        slots[k] = Object.assign({}, slots[k] || {}, cutTechniques);
        ov.cutTech = slots; project.overrides[li] = ov;
      }
      plan = J.plan(project, null);
    }
  }
  if (project.cliForcedFX) {
    const key = project.cliForcedFX;
    if (key !== 'none' && (!J.FXE[key] || J.FXE[key].special))
      throw new Error(`Unknown screen effect fx=${key}; see --list-techniques --tech-group fx`);
    // Screen effects are timed events, not cutTech picks in upstream. Preserve
    // their native implementation and apply one event per cut when requested.
    plan.events = key === 'none' ? [] : plan.cuts.filter(c => c.line >= 0 && !c.interlude).map(c => {
      const D = J.FXE[key];
      return { t: Math.max(0, c.start - (D.pre || 0) / 24), type:key,
        amp: D.amp || 1, dur: (D.dur || 3) / 24 };
    });
  }
  // Saved JSON records uploaded font metadata, but does not contain font binaries.
  // Fail clearly rather than silently replacing a customized typeface.
  const usedFonts = new Set(J.fontsOfPlan(plan));
  for (const uf of project.userFonts || []) {
    if (!usedFonts.has(uf.key)) continue;
    if ((uf.source === 'file' || uf.key.startsWith('user_UF_') || (uf.key.startsWith('user_cli_') && uf.source !== 'family')) && !J.FONTS[uf.key]?.loaded)
      throw new Error(`Project font file ${uf.label || uf.key} is missing; pass --font/--font-display/--font-serif/--font-body`);
  }
  if (!plan || !plan.cuts || !plan.cuts.length) throw new Error('No drawable cuts');
  if (!Number.isFinite(plan.duration) || plan.duration <= 0) throw new Error('Invalid plan duration');
  const [dw, dh] = J.outputSize(project);
  const w = opts.width || dw, h = opts.height || dh;
  if (Math.abs(w / h - plan.W / plan.H) > 0.006)
    throw new Error(`Frame ratio ${w}x${h} does not match ${project.aspect}`);
  const start = opts.start || 0;
  const duration = opts.duration || Math.max(1 / project.fps, plan.duration - start);
  const count = Math.max(1, Math.round(duration * project.fps));
  if (count > opts.maxFrames) throw new Error(`Too many frames (${count}; limit ${opts.maxFrames})`);
  const canvas = document.createElement('canvas');
  canvas.width = w; canvas.height = h;
  const ctx = canvas.getContext('2d', {alpha: true});
  if (!ctx) throw new Error('Canvas 2D unavailable');
  const renderer = new J.Renderer();
  const scale = w / plan.W;
  if (J.glyphs) J.glyphs.maxRes = h >= 1000 ? 768 : 512;
  // External fonts are optional and may be blocked offline. Use installed fallbacks.
  await Promise.race([document.fonts.ready.catch(() => {}),
    new Promise(resolve => setTimeout(resolve, 1500))]);
  globalThis.__jizuraFrame = i => {
    if (!Number.isInteger(i) || i < 0 || i >= count) throw new Error('Frame index out of range');
    renderer.frame(ctx, plan, start + i / project.fps, {scale, transparent: opts.alpha});
    return canvas.toDataURL('image/png');
  };
  return {frames:count, width:w, height:h, fps:project.fps,
    duration:count / project.fps, transparent:opts.alpha,
    planDuration:plan.duration, project:opts.writeProject ? project : null};
}
