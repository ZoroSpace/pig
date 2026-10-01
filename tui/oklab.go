// tui/oklab.go (pi: packages/tui/src/oklab.ts) — Oklab and OKHSL <-> sRGB
// conversion. colors.go builds its OKLCH, OKHSL, and color mixing on it.
//
// Oklab and OKHSL are Björn Ottosson's color spaces; OKHSL's saturation is
// relative to the sRGB gamut at each hue and lightness. This is a port of his
// reference implementation (https://bottosson.github.io/posts/colorpicker/),
// Copyright (c) 2021 Björn Ottosson, used under the MIT license:
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to
// deal in the Software without restriction, including without limitation the
// rights to use, copy, modify, merge, publish, distribute, sublicense, and/or
// sell copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions: The above copyright
// notice and this permission notice shall be included in all copies or
// substantial portions of the Software. THE SOFTWARE IS PROVIDED "AS IS",
// WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.
package tui

import "math"

type vector3 = [3]float64
type matrix3 = [3]vector3

func multiplyMatrix(m matrix3, v vector3) vector3 {
	var out vector3
	for i, row := range m {
		out[i] = row[0]*v[0] + row[1]*v[1] + row[2]*v[2]
	}
	return out
}

// ============================================================================
// OKHSL <-> sRGB
// ============================================================================

var linearSrgbToLms = matrix3{
	{0.4122214694707629, 0.5363325372617349, 0.0514459932675022},
	{0.2119034958178251, 0.6806995506452344, 0.1073969535369405},
	{0.0883024591900564, 0.2817188391361215, 0.6299787016738222},
}
var lmsToLab = matrix3{
	{0.210454268309314, 0.793617774702305, -0.0040720430116193},
	{1.9779985324311684, -2.42859224204858, 0.450593709617411},
	{0.0259040424655478, 0.7827717124575296, -0.8086757549230774},
}
var labToLms = matrix3{
	{1, 0.3963377773761749, 0.2158037573099136},
	{1, -0.1055613458156586, -0.0638541728258133},
	{1, -0.0894841775298119, -1.2914855480194092},
}
var lmsToLinearSrgb = matrix3{
	{4.0767416360759583, -3.3077115392580629, 0.2309699031821043},
	{-1.2684379732850315, 2.6097573492876882, -0.341319376002657},
	{-0.0041960761386756, -0.7034186179359362, 1.7076146940746117},
}

// saturationFitHalfPlanes: per sRGB channel (red, green, blue), the (a, b)
// half-plane where that channel clips first.
var saturationFitHalfPlanes = [3][2]float64{
	{-1.8817031, -0.80936501},
	{1.8144408, -1.19445267},
	{0.13110758, 1.81333971},
}

// saturationFitCoefficients: per sRGB channel, the polynomial approximating the
// maximum saturation in that half-plane.
var saturationFitCoefficients = [3][5]float64{
	{1.19086277, 1.76576728, 0.59662641, 0.75515197, 0.56771245},
	{0.73956515, -0.45954404, 0.08285427, 0.12541073, -0.14503204},
	{1.35733652, -0.00915799, -1.1513021, -0.50559606, 0.00692167},
}

const (
	okhslK1 = 0.206
	okhslK2 = 0.03
	okhslK3 = (1 + okhslK1) / (1 + okhslK2)
)

// OklabToOkhslLightness converts Oklab lightness to OKHSL lightness.
func OklabToOkhslLightness(x float64) float64 {
	t := okhslK3*x - okhslK1
	return 0.5 * (t + math.Sqrt(t*t+4*okhslK2*okhslK3*x))
}

// okhslToOklabLightness converts OKHSL lightness to Oklab lightness.
func okhslToOklabLightness(x float64) float64 {
	return (x*x + okhslK1*x) / (okhslK3 * (x + okhslK2))
}

// linearToSrgb is the sRGB transfer function: linear to encoded channel, both 0-1.
func linearToSrgb(value float64) float64 {
	if value > 0.0031308 {
		return 1.055*math.Pow(value, 1/2.4) - 0.055
	}
	return 12.92 * value
}

// srgbToLinear is the inverse sRGB transfer function: encoded to linear channel, both 0-1.
func srgbToLinear(value float64) float64 {
	if value <= 0.04045 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
}

// OklabToLinearSrgb converts Oklab [L, a, b] to linear sRGB [r, g, b]
// (0-1, may leave the gamut).
func OklabToLinearSrgb(lab vector3) vector3 {
	lms := multiplyMatrix(labToLms, lab)
	cubed := vector3{lms[0] * lms[0] * lms[0], lms[1] * lms[1] * lms[1], lms[2] * lms[2] * lms[2]}
	return multiplyMatrix(lmsToLinearSrgb, cubed)
}

// linearSrgbToOklab converts linear sRGB [r, g, b] (0-1) to Oklab [L, a, b].
func linearSrgbToOklab(rgbv vector3) vector3 {
	lms := multiplyMatrix(linearSrgbToLms, rgbv)
	return multiplyMatrix(lmsToLab, vector3{math.Cbrt(lms[0]), math.Cbrt(lms[1]), math.Cbrt(lms[2])})
}

// RgbToOklab converts sRGB channels (0-255) to Oklab [L, a, b].
func RgbToOklab(c RgbColor) vector3 {
	linear := vector3{srgbToLinear(float64(c.R) / 255), srgbToLinear(float64(c.G) / 255), srgbToLinear(float64(c.B) / 255)}
	return linearSrgbToOklab(linear)
}

// LinearSrgbToRgb converts linear sRGB [r, g, b] to sRGB channels (0-255,
// rounded), clipping out-of-gamut channels.
func LinearSrgbToRgb(linear vector3) RgbColor {
	encode := func(value float64) int {
		if value < 0 {
			value = 0
		}
		if value > 1 {
			value = 1
		}
		return int(math.Round(linearToSrgb(value) * 255))
	}
	return RgbColor{R: encode(linear[0]), G: encode(linear[1]), B: encode(linear[2])}
}

// lmsSlopes is the rate of change of each cube-root LMS component along a
// chroma direction (a, b).
func lmsSlopes(a, b float64) vector3 {
	var out vector3
	for i, row := range labToLms {
		out[i] = row[1]*a + row[2]*b
	}
	return out
}

// maxSaturation is the largest saturation (C/L) inside sRGB for hue (a, b):
// polynomial fit plus one Halley step.
func maxSaturation(a, b float64) float64 {
	channel := 2
	for i := 0; i < 2; i++ {
		half := saturationFitHalfPlanes[i]
		if half[0]*a+half[1]*b > 1 {
			channel = i
			break
		}
	}
	coeffs := saturationFitCoefficients[channel]
	k0, k1, k2, k3, k4 := coeffs[0], coeffs[1], coeffs[2], coeffs[3], coeffs[4]
	weights := lmsToLinearSrgb[channel]
	saturation := k0 + k1*a + k2*b + k3*a*a + k4*a*b

	slopes := lmsSlopes(a, b)
	var base vector3
	for i, k := range slopes {
		base[i] = 1 + saturation*k
	}
	dot := func(values vector3) float64 {
		sum := 0.0
		for i, v := range values {
			sum += weights[i] * v
		}
		return sum
	}
	cubes := vector3{base[0] * base[0] * base[0], base[1] * base[1] * base[1], base[2] * base[2] * base[2]}
	var f1v, f2v vector3
	for i := range base {
		f1v[i] = 3 * slopes[i] * base[i] * base[i]
		f2v[i] = 6 * slopes[i] * slopes[i] * base[i]
	}
	f := dot(cubes)
	f1 := dot(f1v)
	f2 := dot(f2v)
	return saturation - (f*f1)/(f1*f1-0.5*f*f2)
}

// cusp is the Oklab lightness and chroma of the most saturated sRGB color of
// hue (a, b).
func cusp(a, b float64) (float64, float64) {
	saturation := maxSaturation(a, b)
	linear := OklabToLinearSrgb(vector3{1, saturation * a, saturation * b})
	extreme := math.Max(linear[0], math.Max(linear[1], linear[2]))
	lightness := math.Cbrt(1 / extreme)
	return lightness, lightness * saturation
}

// maxChroma is the chroma where the constant-lightness line at `lightness`
// leaves the sRGB gamut.
func maxChroma(a, b, lightness, cuspL, cuspC float64) float64 {
	if lightness <= cuspL {
		return (cuspC * lightness) / cuspL
	}
	// Upper half: triangle edge, then one Halley step against each channel reaching 1.
	t := (cuspC * (lightness - 1)) / (cuspL - 1)
	slopes := lmsSlopes(a, b)
	var lms, cubes, first, second vector3
	for i, k := range slopes {
		lms[i] = lightness + t*k
		cubes[i] = lms[i] * lms[i] * lms[i]
		first[i] = 3 * k * lms[i] * lms[i]
		second[i] = 6 * k * k * lms[i]
	}
	dot := func(row vector3, values vector3) float64 {
		return row[0]*values[0] + row[1]*values[1] + row[2]*values[2]
	}
	minStep := math.MaxFloat64
	for _, row := range lmsToLinearSrgb {
		f := dot(row, cubes) - 1
		f1 := dot(row, first)
		f2 := dot(row, second)
		u := f1 / (f1*f1 - 0.5*f*f2)
		if u >= 0 {
			step := -f * u
			if step < minStep {
				minStep = step
			}
		}
	}
	return t + minStep
}

// chromaStops is OKHSL's chroma reference points at lightness L and hue
// (a, b): [c0, cMid, cMax].
func chromaStops(L, a, b float64) (float64, float64, float64) {
	cuspL, cuspC := cusp(a, b)
	cMax := maxChroma(a, b, L, cuspL, cuspC)
	k := cMax / math.Min(L*(cuspC/cuspL), (1-L)*(cuspC/(1-cuspL)))
	midS := 0.11516993 +
		1/
			(7.4477897+
				4.1590124*b+
				a*(-2.19557347+
					1.75198401*b+
					a*(-2.13704948-10.02301043*b+a*(-4.24894561+5.38770819*b+4.69891013*a))))
	midT := 0.11239642 +
		1/
			(1.6132032-
				0.68124379*b+
				a*(0.40370612+
					0.90148123*b+
					a*(-0.27087943+0.6122399*b+a*(0.00299215-0.45399568*b-0.14661872*a))))
	cMid := 0.9 * k * math.Sqrt(math.Sqrt(1/(1/math.Pow(L*midS, 4)+1/math.Pow((1-L)*midT, 4))))
	c0 := math.Sqrt(1 / (1/math.Pow(L*0.4, 2) + 1/math.Pow((1-L)*0.8, 2)))
	return c0, cMid, cMax
}

// OkhslToRgb converts OKHSL to sRGB channels (0-255, rounded), clipping
// out-of-gamut channels.
//   - hue: hue in degrees.
//   - saturation: saturation, 0-1.
//   - lightness: lightness, 0-1.
func OkhslToRgb(hue, saturation, lightness float64) RgbColor {
	L := okhslToOklabLightness(lightness)
	lab := vector3{L, 0, 0}
	if L > 0 && L < 1 && saturation > 0 {
		normalizedHue := math.Mod(math.Mod(hue, 360)+360, 360)
		angle := (2 * math.Pi * normalizedHue) / 360
		a := math.Cos(angle)
		b := math.Sin(angle)
		c0, cMid, cMax := chromaStops(L, a, b)
		// Chroma rises from 0 through cMid at s = 0.8 to cMax at s = 1.
		var chroma float64
		if saturation < 0.8 {
			t := 1.25 * saturation
			k1 := 0.8 * c0
			chroma = (t * k1) / (1 - (1-k1/cMid)*t)
		} else {
			t := 5 * (saturation - 0.8)
			k1 := (0.2 * cMid * cMid * 1.25 * 1.25) / c0
			chroma = cMid + (t*k1)/(1-(1-k1/(cMax-cMid))*t)
		}
		lab = vector3{L, chroma * a, chroma * b}
	}
	return LinearSrgbToRgb(OklabToLinearSrgb(lab))
}

// OkhslChannels holds OKHSL channels: hue h in degrees, saturation s and
// lightness l 0-1.
type OkhslChannels struct {
	H float64
	S float64
	L float64
}

// RgbToOkhsl converts sRGB channels (0-255) to OKHSL. Hue is in degrees
// (0 for grays); saturation and lightness are 0-1.
func RgbToOkhsl(c RgbColor) OkhslChannels {
	lab := RgbToOklab(c)
	L, labA, labB := lab[0], lab[1], lab[2]
	chroma := math.Hypot(labA, labB)
	lightness := OklabToOkhslLightness(L)
	if chroma < 1e-9 || lightness <= 0 || lightness >= 1 {
		return OkhslChannels{H: 0, S: 0, L: lightness}
	}

	hue := math.Mod((math.Atan2(labB, labA)*180/math.Pi)+360, 360)
	c0, cMid, cMax := chromaStops(L, labA/chroma, labB/chroma)
	var saturation float64
	if chroma < cMid {
		k1 := 0.8 * c0
		saturation = 0.8 * (chroma / (k1 + (1-k1/cMid)*chroma))
	} else {
		k1 := (0.2 * cMid * cMid * 1.25 * 1.25) / c0
		offset := chroma - cMid
		saturation = 0.8 + 0.2*(offset/(k1+(1-k1/(cMax-cMid))*offset))
	}
	saturation = math.Min(1, math.Max(0, saturation))
	return OkhslChannels{H: hue, S: saturation, L: lightness}
}
