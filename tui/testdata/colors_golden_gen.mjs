import * as oklab from '/Users/lee/Projects/pi/packages/tui/src/oklab.ts';
import * as colors from '/Users/lee/Projects/pi/packages/tui/src/colors.ts';
import * as tc from '/Users/lee/Projects/pi/packages/tui/src/terminal-colors.ts';

const out = { okhslToRgb: [], rgbToOkhsl: [], rgbToOklab: [], oklabLightness: [], parse: [], toHex: [], toOklch: [], toOkhsl: [], toRgb: [], mix: [], ansi256: [], style: [], osc: [] };

// okhsl -> rgb over a grid
const hues = [0, 20, 52, 82.36, 158.68, 202.43, 231.49, 263.25, 295, 337.5, 360, 400];
for (const h of hues) for (const s of [0, 0.1, 0.3, 0.5, 0.8, 0.95, 1]) for (const l of [0, 0.05, 0.2, 0.5, 0.8, 0.95, 1]) {
  out.okhslToRgb.push([h, s, l, oklab.okhslToRgb(h, s, l)]);
}
// rgb -> okhsl + oklab
const rgbs = [[0,0,0],[255,255,255],[128,128,128],[255,0,0],[0,255,0],[0,0,255],[228,138,122],[79,142,179],[234,182,93],[1,2,3],[254,253,252],[95,135,175],[8,8,8],[238,238,238],[0x5f,0x87,0xd7]];
for (const [r,g,b] of rgbs) {
  out.rgbToOkhsl.push([r,g,b, oklab.rgbToOkhsl({r,g,b})]);
  out.rgbToOklab.push([r,g,b, oklab.rgbToOklab({r,g,b})]);
}
// oklab lightness round trip
for (const x of [0, 0.1, 0.25, 0.5, 0.75, 0.9, 1]) out.oklabLightness.push([x, oklab.oklabToOkhslLightness(x)]);

// parseColor + hex
const values = ['#fff','#ffffff','#000000','#5f87d7','oklch(0.5 0.1 120)','oklch(50% 0.1 120deg)','oklch(0.9 0.02 300.5)','okhsl(120 0.5 0.5)','okhsl(120deg 50% 50%)','okhsl(231.49 0.68 0.5)'];
for (const v of values) {
  const c = colors.parseColor(v);
  out.parse.push([v, c]);
  out.toHex.push(colors.colorToHex(c));
  out.toOklch.push(colors.colorToOklch(c));
  out.toOkhsl.push(colors.colorToOkhsl(c));
  out.toRgb.push(colors.colorToRgb(c));
}
// indexed colors
for (const i of [0,1,7,8,15,16,17,59,109,123,196,231,232,243,255]) {
  const c = colors.parseColor(i);
  out.parse.push([i, c]);
  out.toRgb.push(colors.colorToRgb(c));
  out.toHex.push(colors.colorToHex(c));
  out.toOklch.push(colors.colorToOklch(c));
  out.toOkhsl.push(colors.colorToOkhsl(c));
}
// mix
const a = colors.parseColor('#ff0000'), b = colors.parseColor('#0000ff');
for (const amt of [0,0.25,0.5,0.75,1]) {
  out.mix.push([amt, colors.colorToHex(colors.mixColors(a,b,amt)), colors.colorToHex(colors.mixColors(a,b,amt,'srgb')), colors.colorToOklch(colors.mixColors(a,b,amt))]);
}
const gray = colors.parseColor('#808080');
out.mix.push(['gray', colors.colorToOklch(colors.mixColors(gray,b,0.5))]);
// ansi256
for (const rgbv of [[0,0,0],[255,0,0],[128,128,128],[228,138,122],[79,142,179],[234,182,93],[253,254,255],[1,1,1],[127,127,127],[250,240,230]]) {
  out.ansi256.push([rgbv, colors.foregroundAnsi(colors.rgbColor(...rgbv), '256color'), colors.foregroundAnsi(colors.rgbColor(...rgbv), 'truecolor')]);
}
// styleText
const attrs = [{},{bold:true},{dim:true},{italic:true},{underline:true},{inverse:true},{strikethrough:true},{bold:true,italic:true,underline:true}];
for (const at of attrs) {
  out.style.push([at, colors.styleText('x', {fg: a, bg: b, ...at}, 'truecolor')]);
}
out.styleWithAnsi = attrs.map(at => colors.styleTextWithAnsi('y', '\x1b[38;2;1;2;3m', '\x1b[48;2;9;8;7m', at));

// terminal colors parsing
const replies = ['\x1b]10;rgb:cccc/cccd/cccc\x07','\x1b]11;rgb:0000/0000/0000\x1b\\','\x1b]4;1;rgb:ffff/5555/5555\x07','\x1b]11;#1e1e2e\x07','\x1b]10;rgba:ffff/ffff/ffff/ffff\x07','\x1b]4;2;rgb:ffff/ffff/ffff\x07'];
for (const r of replies) out.osc.push([r, tc.parseOscColorResponse(r)]);
out.scheme = [tc.parseTerminalColorSchemeReport('\x1b[?997;1n'), tc.parseTerminalColorSchemeReport('\x1b[?997;2n'), tc.parseTerminalColorSchemeReport('\x1b[?997;2n\x1b[?997;2n')];

console.log(JSON.stringify(out));
