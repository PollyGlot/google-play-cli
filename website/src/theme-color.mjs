// Browser-chrome colour (the theme-color meta) per resolved theme, shared by
// the Starlight head config (astro.config.mjs) and the landing page's
// first-paint and theme-control scripts. It paints the mobile address bar to
// match the page surface, so it must equal --gplay-ink and --light-surface in
// src/styles/theme.css (CSS cannot import it, hence the two declarations).
export const THEME_COLOR = Object.freeze({
  dark: '#050507',
  light: '#ffffff',
});
