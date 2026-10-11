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
  const plan = J.plan(project, null);
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
