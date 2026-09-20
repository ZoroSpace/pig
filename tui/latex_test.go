// pi: packages/tui/test/latex.test.ts — test-for-test port of renderLatex.
package tui

import "testing"

type latexCase struct {
	source   string
	expected string
	display  bool
}

func runLatexCases(t *testing.T, cases []latexCase) {
	t.Helper()
	for _, c := range cases {
		got, ok := RenderLatex(c.source, c.display)
		if !ok {
			t.Errorf("RenderLatex(%q) = unsupported, want %q", c.source, c.expected)
			continue
		}
		if got != c.expected {
			t.Errorf("RenderLatex(%q) = %q, want %q", c.source, got, c.expected)
		}
	}
}

func TestRenderLatexJacobianDollar(t *testing.T) {
	runLatexCases(t, []latexCase{
		{`\mathbb{C}^3 \to \mathbb{C}^3`, "ℂ³ → ℂ³", false},
		{`\{3x+2y,\; 27x^2-4z-1,\; x(x-1)(x+1)\} \quad\Rightarrow\quad x \in \{0, \pm 1\},`, "{3x+2y, 27x²-4z-1, x(x-1)(x+1)} ⇒ x ∈ {0, ± 1},", false},
		{`F_1 = -\frac{1}{4x^2}.`, "F₁ = -1/(4x²).", false},
		{"-2", "-2", false},
		{"(0,0,-1/4)", "(0,0,-1/4)", false},
		{"(1,-3/2,13/2)", "(1,-3/2,13/2)", false},
		{"(1,1,1)", "(1,1,1)", false},
		{"(2,1,0)", "(2,1,0)", false},
		{"(-1/4, 0, 0)", "(-1/4, 0, 0)", false},
		{`\{(0,0,-1/4), (1,-3/2,13/2), (-1,3/2,13/2)\}`, "{(0,0,-1/4), (1,-3/2,13/2), (-1,3/2,13/2)}", false},
		{"(2,1,1)", "(2,1,1)", false},
		{"(7/3,-2/5,11/7)", "(7/3,-2/5,11/7)", false},
		{`\{y - p(x),\; q(x)\}`, "{y - p(x), q(x)}", false},
		{`\deg q = 3`, "deg q = 3", false},
		{`[\mathbb{C}(x,y,z):\mathbb{C}(F_1,F_2,F_3)] = 3`, "[ℂ(x,y,z):ℂ(F₁,F₂,F₃)] = 3", false},
		{"u = 1+xy", "u = 1+xy", false},
		{"G = u^2 z + y^2(4+3xy)", "G = u² z + y²(4+3xy)", false},
		{"F_1 = uG", "F₁ = uG", false},
		{"F_2 = y + 3xG", "F₂ = y + 3xG", false},
		{"x=0", "x = 0", false},
		{"F_2 = F_3 = 0", "F₂ = F₃ = 0", false},
		{"xy = -3/2", "xy = -3/2", false},
		{"x^2 z = 13/2", "x² z = 13/2", false},
		{`\mathbb{C}^*`, "ℂ^*", false},
		{`s \mapsto (s,\, -\tfrac{3}{2s},\, \tfrac{13}{2s^2})`, "s ↦ (s, -3/(2s), 13/(2s²))", false},
		{"X", "X", false},
		{`p_\pm`, "p_±", false},
		{"F(-x,-y,z) = (F_1, -F_2, -F_3)", "F(-x,-y,z) = (F₁, -F₂, -F₃)", false},
		{"p_0", "p₀", false},
		{`s \to \infty`, "s → ∞", false},
		{"(0,0,0)", "(0,0,0)", false},
		{`\Rightarrow`, "⇒", false},
		{`\ge 2`, "≥ 2", false},
		{`\ge 3`, "≥ 3", false},
		{"1", "1", false},
		{`\mathrm{diag}(-1/2,1,1),\quad F_{\rm intrinsic}(\lambda)`, "diag(-1/2,1,1), F_intrinsic(λ)", false},
		{"4+3xy", "4+3xy", false},
	})
}

func TestRenderLatexSatelliteBracket(t *testing.T) {
	runLatexCases(t, []latexCase{
		{`E \approx \frac{0.1\ \text{lux}}{100\ \text{lm/W}} = 0.001\ \text{W/m}^2`, "E ≈ (0.1 lux)/(100 lm/W) = 0.001 W/m²", false},
		{`\boxed{1\ \text{milliwatt per square metre}}`, "[1 milliwatt per square metre]", false},
		{`5\ \text{km}^2 = 5{,}000{,}000\ \text{m}^2`, "5 km² = 5,000,000 m²", false},
		{"P_{\\text{light}} = 0.001 \\times 5{,}000{,}000\n= \\boxed{5{,}000\\ \\text{W}}", "P_light = 0.001 × 5,000,000 = [5,000 W]", false},
		{"P_{\\text{electric}} = 5\\ \\text{kW} \\times 0.2\n= \\boxed{1\\ \\text{kW}}", "P_electric = 5 kW × 0.2 = [1 kW]", false},
		{`\pi(2.5\ \text{km})^2 = 19.6\ \text{km}^2`, "π(2.5 km)² = 19.6 km²", false},
		{"0.001\\ \\text{W/m}^2 \\times 19.6 \\times 10^6\\ \\text{m}^2\n\\approx \\boxed{20\\ \\text{kW optical}}", "0.001 W/m² × 19.6 × 10⁶ m² ≈ [20 kW optical]", false},
		{"1\\ \\text{kW} \\times \\frac{1}{3600}\\ \\text{hour}\n= \\boxed{0.28\\ \\text{Wh}}", "1 kW × 1/3600 hour = [0.28 Wh]", false},
	})
}

func TestRenderLatexJacobianParenthesisAndBracket(t *testing.T) {
	runLatexCases(t, []latexCase{
		{`\det\!\left(\frac{\partial(F_1,F_2,F_3)}{\partial(x,y,z)}\right)=-2.`, "det((∂(F₁,F₂,F₃))/(∂(x,y,z))) = -2.", false},
		{"\\begin{aligned}\nF(0,0,-\\tfrac14)&=(-\\tfrac14,0,0),\\\\\nF(1,-\\tfrac32,\\tfrac{13}2)&=(-\\tfrac14,0,0),\\\\\nF(-1,\\tfrac32,\\tfrac{13}2)&=(-\\tfrac14,0,0).\n\\end{aligned}", "F(0,0,-1/4) = (-1/4,0,0),\nF(1,-3/2,13/2) = (-1/4,0,0),\nF(-1,3/2,13/2) = (-1/4,0,0).", false},
		{"F=(F_1,F_2,F_3)", "F = (F₁,F₂,F₃)", false},
		{"F", "F", false},
		{"3", "3", false},
	})
}

func TestRenderLatexJacobianMatrix(t *testing.T) {
	runLatexCases(t, []latexCase{
		{`J = \begin{pmatrix}
\frac{\partial f_1}{\partial x} & \frac{\partial f_1}{\partial y} & \frac{\partial f_1}{\partial z} \\
\frac{\partial f_2}{\partial x} & \frac{\partial f_2}{\partial y} & \frac{\partial f_2}{\partial z} \\
\frac{\partial f_3}{\partial x} & \frac{\partial f_3}{\partial y} & \frac{\partial f_3}{\partial z}
\end{pmatrix}`,
			"J = ⎛ (∂ f₁)/(∂ x) │ (∂ f₁)/(∂ y) │ (∂ f₁)/(∂ z) ⎞\n    ⎜ (∂ f₂)/(∂ x) │ (∂ f₂)/(∂ y) │ (∂ f₂)/(∂ z) ⎟\n    ⎝ (∂ f₃)/(∂ x) │ (∂ f₃)/(∂ y) │ (∂ f₃)/(∂ z) ⎠", false},
		{`\begin{aligned}
f_1 &= (1+xy)^3 z + y^2(1+xy)(4+3xy) \\
f_2 &= y + 3x(1+xy)^2 z + 3xy^2(4+3xy) \\
f_3 &= 2x - 3x^2y - x^3z
\end{aligned}`,
			"f₁ = (1+xy)³ z + y²(1+xy)(4+3xy)\nf₂ = y + 3x(1+xy)² z + 3xy²(4+3xy)\nf₃ = 2x - 3x²y - x³z", false},
		{"x, y, z", "x, y, z", false},
		{"(x, y, z)", "(x, y, z)", false},
		{`(0,\; 0,\; -\tfrac14)`, "(0, 0, -1/4)", false},
		{`(-\tfrac14,\; 0,\; 0)`, "(-1/4, 0, 0)", false},
		{`(1,\; -\tfrac32,\; \tfrac{13}{2})`, "(1, -3/2, 13/2)", false},
		{`(-1,\; \tfrac32,\; \tfrac{13}{2})`, "(-1, 3/2, 13/2)", false},
		{`(-\frac14, 0, 0)`, "(-1/4, 0, 0)", false},
		{`F: \mathbb{C}^3 \to \mathbb{C}^3`, "F: ℂ³ → ℂ³", false},
		{`F(0,0,-\tfrac14) = F(1,-\tfrac32,\tfrac{13}{2}) = F(-1,\tfrac32,\tfrac{13}{2}) = (-\tfrac14, 0, 0)`,
			"F(0,0,-1/4) = F(1,-3/2,13/2) = F(-1,3/2,13/2) = (-1/4, 0, 0)", false},
		{`\mathbb{C}^3`, "ℂ³", false},
		{`\begin{aligned}
f_1 &= \frac{f_1^{\text{ut}}(u,t)}{x^2}, \quad
f_2 = \frac{f_2^{\text{ut}}(u,t)}{x}, \quad
f_3 = x\,(2 - 3u - t)
\end{aligned}`,
			"f₁ = (f₁ᵘᵗ(u,t))/(x²), f₂ = (f₂ᵘᵗ(u,t))/x, f₃ = x (2 - 3u - t)", false},
		{`\det J_F`, "det J_F", false},
		{`(-\tfrac14, 0, 0)`, "(-1/4, 0, 0)", false},
		{"u = xy", "u = xy", false},
		{"t = x^2z", "t = x²z", false},
		{`x \neq 0`, "x ≠ 0", false},
		{`f_1^{\text{ut}}, f_2^{\text{ut}}`, "f₁ᵘᵗ, f₂ᵘᵗ", false},
		{"u,t", "u,t", false},
		{"x", "x", false},
		{"x, x^2", "x, x²", false},
		{`\mathbb{C}^n \to \mathbb{C}^n`, "ℂⁿ → ℂⁿ", false},
		{`n \geq 2`, "n ≥ 2", false},
		{`\mathbb{P}^3`, "ℙ³", false},
	})
}

func TestRenderLatexStressTest(t *testing.T) {
	runLatexCases(t, []latexCase{
		{`e^{i\pi}+1=0`, "e^(iπ)+1 = 0", false},
		{`\boxed{
\mathcal{Z}(\beta)
=
\int_{\mathcal M}
\exp\!\left(
-\beta\left[
\frac12 g^{ij}(x)\,\partial_i\phi\,\partial_j\phi
+V(\phi)
\right]\right)
\mathcal D\phi
}`,
			"[Z(β) = ∫_M exp( -β[ 1/2 gⁱʲ(x) ∂ᵢϕ ∂ⱼϕ +V(ϕ) ]) Dϕ]", false},
		{`\begin{aligned}
\nabla_\mu T^{\mu\nu}
&=
\frac{1}{\sqrt{-g}}
\partial_\mu\!\left(\sqrt{-g}\,T^{\mu\nu}\right)
+\Gamma^\nu_{\mu\lambda}T^{\mu\lambda}
=0, \\[4pt]
R_{\mu\nu}-\frac12 Rg_{\mu\nu}+\Lambda g_{\mu\nu}
&=
\frac{8\pi G}{c^4}T_{\mu\nu}.
\end{aligned}`,
			"∇_μ T^(μν) = 1/(√(-g)) ∂_μ(√(-g) T^(μν)) +Γ^ν_(μλ)T^(μλ) = 0,\nR_(μν)-1/2 Rg_(μν)+Λ g_(μν) = (8π G)/(c⁴)T_(μν).", false},
		{`f(z)
=
\frac{1}{2\pi i}
\oint_{\gamma}
\frac{f(\zeta)}{\zeta-z}\,d\zeta,
\qquad
\det\!\begin{pmatrix}
\lambda-a & -b & 0\\
-c & \lambda-d & -e\\
0 & -f & \lambda-g
\end{pmatrix}
=0.`,
			"f(z) = 1/(2π i) ∮_γ (f(ζ))/(ζ-z) dζ, det⎛ λ-a │ -b  │ 0   ⎞ = 0.\n" + repeatSpaces(40) + "⎜ -c  │ λ-d │ -e  ⎟\n" + repeatSpaces(40) + "⎝ 0   │ -f  │ λ-g ⎠", false},
		{`\Psi(x,t)=
\sum_{n=1}^{\infty}
\underbrace{
c_n
\sqrt{\frac{2}{L}}
\sin\!\left(\frac{n\pi x}{L}\right)
}_{\text{spatial eigenmode}}
\exp\!\left(-\frac{i\hbar n^2\pi^2}{2mL^2}t\right),
\qquad
|\Psi(x,t)|^2
=
\begin{cases}
\Psi^\ast\Psi, & 0<x<L,\\
0, & \text{otherwise}.
\end{cases}`,
			"                                                                                                 ⎧ Ψ^∗Ψ if 0 < x < L,\nΨ(x,t) = ∑ₙ₌₁^∞ cₙ √(2/L) sin((nπ x)/L)_(spatial eigenmode) exp(-(iℏ n²π²)/(2mL²)t), |Ψ(x,t)|² = ⎨\n                                                                                                 ⎩ 0    otherwise.", false},
		{`x=\frac{-b\pm\sqrt{b^2-4ac}}{2a}`, "x = (-b±√(b²-4ac))/(2a)", false},
		{`\int_0^\infty e^{-x^2}\,dx=\frac{\sqrt{\pi}}{2}`, "∫₀^∞ e^(-x²) dx = (√π)/2", false},
		{`e^{i\theta}=\cos\theta+i\sin\theta`, "e^(iθ) = cos θ+i sin θ", false},
		{`\sum_{n=1}^{\infty}\frac{1}{n^2}=\frac{\pi^2}{6}`, "∑ₙ₌₁^∞1/(n²) = π²/6", false},
		{`\lim_{x\to 0}\frac{\sin x}{x}=1`, "lim[x→0] (sin x)/x = 1", false},
		{`\lim_{n\to\infty}
\left(1+\frac{1}{n}\right)^n=e`, "lim[n→∞] (1+1/n)ⁿ = e", false},
		{`\int_0^1 \frac{x^2}{1+x^3}\,dx
=\frac{1}{3}\ln 2`, "∫₀¹ x²/(1+x³) dx = 1/3 ln 2", false},
		{`\sum_{k=1}^{n}\frac{k}{k+1}
=n+1-H_{n+1}`, "∑ₖ₌₁ⁿk/(k+1) = n+1-Hₙ₊₁", false},
		{`\frac{
  \displaystyle \frac{x^2+1}{x-1}
  -
  \displaystyle \frac{2x}{x+1}
}{
  \displaystyle \frac{x}{x^2-1}
}`, "((x²+1)/(x-1) - 2x/(x+1))/(x/(x²-1))", false},
		{`\lim_{x\to 0}
\frac{
  \displaystyle \frac{\sin x}{x}-1
}{
  \displaystyle \frac{e^x-1}{x}-1
}
=0`, "lim[x→0] ((sin x)/x-1)/((eˣ-1)/x-1) = 0", false},
		{`\frac{
  1+\displaystyle\frac{1}{1+\frac{1}{x}}
}{
  1-\displaystyle\frac{1}{1-\frac{1}{x}}
}`, "(1+1/(1+1/x))/(1-1/(1-1/x))", false},
		{`\sum_{n=1}^{\infty}
\frac{
  \displaystyle \frac{1}{n}-\frac{1}{n+1}
}{
  \displaystyle 1+\frac{1}{n^2}
}`, "∑ₙ₌₁^∞ (1/n-1/(n+1))/(1+1/(n²))", false},
	})
}

func repeatSpaces(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = ' '
	}
	return string(out)
}

func TestRenderLatexCommonSymbols(t *testing.T) {
	got, ok := RenderLatex(`\sum_{i=0}^n \alpha_i + \int_0^\infty e^{-x^2}\,dx = \sqrt{\pi}`, false)
	if !ok || got != "∑ᵢ₌₀ⁿ αᵢ + ∫₀^∞ e^(-x²) dx = √π" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestRenderLatexAccentsAndBinomial(t *testing.T) {
	got, ok := RenderLatex(`\binom{n}{k}+\vec{x}+\hat{y}+\overline{AB}`, false)
	// pi's expected literal uses y + COMBINING CIRCUMFLEX ACCENT (U+0302),
	// not the precomposed ŷ — the accent commands append combining marks.
	if !ok || got != "(n choose k)+x⃗+y\u0302+overline(AB)" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestRenderLatexExtendedSymbolsAndNegated(t *testing.T) {
	got, ok := RenderLatex(`\epsilon+\varepsilon+\varsigma+\varkappa+\oplus+\otimes+\therefore+\because`, false)
	if !ok || got != "ϵ+ε+ς+ϰ+⊕+⊗+∴+∵" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	got, ok = RenderLatex(`A\not\subseteq B,\quad x\not\in X`, false)
	if !ok || got != "A ⊈ B, x ∉ X" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestRenderLatexRelationalAlgebraJoins(t *testing.T) {
	got, _ := RenderLatex(`R\bowtie S,\quad R\Join S`, false)
	if got != "R ⋈ S, R ⋈ S" {
		t.Errorf("got %q", got)
	}
	got, _ = RenderLatex(`R\ltimes S,\quad R\rtimes S`, false)
	if got != "R ⋉ S, R ⋊ S" {
		t.Errorf("got %q", got)
	}
	got, _ = RenderLatex(`R\leftouterjoin S,\quad R\rightouterjoin S,\quad R\fullouterjoin S`, false)
	if got != "R ⟕ S, R ⟖ S, R ⟗ S" {
		t.Errorf("got %q", got)
	}
}

func TestRenderLatexDelimiters(t *testing.T) {
	got, ok := RenderLatex(`\lvert{x}\rvert+\lVert{v}\rVert+\left.\frac{dy}{dx}\right|_{x=0}`, false)
	if !ok || got != "|x|+‖v‖+dy/(dx)|ₓ₌₀" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	got, ok = RenderLatex(`\left\lbrace x \middle| x>0 \right\rbrace`, false)
	if !ok || got != "{ x | x > 0 }" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestRenderLatexNamedModularOverlaidOperators(t *testing.T) {
	got, ok := RenderLatex(`\operatorname*{arg\,max}_{x\in X} f(x)`, false)
	if !ok || got != "arg max[x∈X] f(x)" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	got, ok = RenderLatex(`a\bmod n,\quad a\equiv b\pmod n`, false)
	if !ok || got != "a mod n, a ≡ b (mod n)" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	got, ok = RenderLatex(`\overset{!}{=}+\underset{n}{x}+\stackrel{def}{=}`, false)
	if !ok || got != "=^!+xₙ+=ᵈᵉᶠ" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestRenderLatexIndexedRootsAndAccents(t *testing.T) {
	got, ok := RenderLatex(`\sqrt[2]{x}+\sqrt[3]{x}+\sqrt[4]{x}+\sqrt[n]{x}+\sqrt[k]{x+1}`, false)
	if !ok || got != "√x+∛x+∜x+ⁿ√x+ᵏ√(x+1)" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	got, ok = RenderLatex(`\acute{x}+\grave{y}+\widehat{xyz}+\overrightarrow{AB}`, false)
	if !ok || got != "x́+ỳ+widehat(xyz)+overrightarrow(AB)" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	got, ok = RenderLatex(`\textnormal{hello}+\mbox{world}+\boldsymbol{x}+{\rm roman}+{\bf bold}+{\it italic}+{\sf sans}+{\tt mono}+{\cal calligraphic}+{\sl slanted}`, false)
	if !ok || got != "hello+world+x+roman+bold+italic+sans+mono+calligraphic+slanted" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestRenderLatexDisplayEnvironments(t *testing.T) {
	got, ok := RenderLatex(`\begin{equation}\begin{split}a&=b\\&=c\end{split}\end{equation}`, false)
	if !ok || got != "a = b\n= c" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	got, ok = RenderLatex(`\begin{alignedat}{2}a&=b&\quad c&=d\\e&=f&g&=h\end{alignedat}`, false)
	if !ok || got != "a = b c = d\ne = f g = h" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestRenderLatexCasesAndMatrixColumns(t *testing.T) {
	got, ok := RenderLatex(`f(x)=\begin{cases}a & x<0 \\ b & \text{if }x=0 \\ c & \text{otherwise}\end{cases}`, false)
	if !ok || got != "       ⎧ a if x < 0\nf(x) = ⎨ b if x = 0\n       ⎩ c otherwise" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	got, ok = RenderLatex(`\begin{pmatrix}1&200\\3000&4\end{pmatrix}`, false)
	if !ok || got != "⎛ 1    │ 200 ⎞\n⎝ 3000 │ 4   ⎠" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestRenderLatexMatricesWithFractionsDisplay(t *testing.T) {
	runLatexCases(t, []latexCase{
		{`R\left(\frac{\pi}{4}\right)
=
\begin{pmatrix}
\frac{\sqrt{2}}{2} & -\frac{\sqrt{2}}{2}\\
\frac{\sqrt{2}}{2} & \frac{\sqrt{2}}{2}
\end{pmatrix}.`,
			"   π\nR( ─ ) = ⎛ (√2)/2 │ -(√2)/2 ⎞\n   4     ⎝ (√2)/2 │ (√2)/2  ⎠.", true},
		{`\mathbf w
=
R\left(\frac{\pi}{4}\right)
\begin{pmatrix}1\\0\end{pmatrix}
=
\begin{pmatrix}\frac{\sqrt{2}}{2}\\\frac{\sqrt{2}}{2}\end{pmatrix}.`,
			"       π\nw = R( ─ ) ⎛ 1 ⎞ = ⎛ (√2)/2 ⎞\n       4   ⎝ 0 ⎠   ⎝ (√2)/2 ⎠.", true},
		{`A\mathbf e_1=\begin{pmatrix}\pi\\0\end{pmatrix},\qquad A\mathbf e_2=\begin{pmatrix}0\\\frac{1}{\pi}\end{pmatrix}.`,
			"Ae₁ = ⎛ π ⎞, Ae₂ = ⎛ 0   ⎞\n      ⎝ 0 ⎠        ⎝ 1/π ⎠.", true},
		{`\sum_{i=0}^n x_i=\begin{pmatrix}a&b\\c&d\end{pmatrix}.`,
			" n\n ∑  xᵢ = ⎛ a │ b ⎞\ni=0      ⎝ c │ d ⎠.", true},
	})
}

func TestRenderLatexSpacingNormalization(t *testing.T) {
	for _, source := range []string{"x=y", "x =y", "x=\ny", "x\n=\ny"} {
		got, ok := RenderLatex(source, false)
		if !ok || got != "x = y" {
			t.Errorf("RenderLatex(%q) = %q ok=%v, want %q", source, got, ok, "x = y")
		}
	}
	cases := []struct{ source, expected string }{
		{"x_{i=0}", "xᵢ₌₀"},
		{`x\neq0`, "x ≠ 0"},
		{`A\to B`, "A → B"},
		{`\pi\cdot\frac{1}{\pi}`, "π · 1/π"},
		{`\sin\theta`, "sin θ"},
		{`\sin^2 x`, "sin² x"},
		{`-\sin\theta`, "-sin θ"},
		{`i\sin\theta`, "i sin θ"},
		{`\det(A)`, "det(A)"},
	}
	for _, c := range cases {
		got, ok := RenderLatex(c.source, false)
		if !ok || got != c.expected {
			t.Errorf("RenderLatex(%q) = %q ok=%v, want %q", c.source, got, ok, c.expected)
		}
	}
}

func TestRenderLatexBackslashLineEndingControlSpace(t *testing.T) {
	source := "\\boxed{\n(1,1,1),\\ (1,1,2),\\ (1,2,5),\\ (1,5,13),\\ (2,5,29),\\\n(1,13,34),\\ (1,34,89)\n}."
	got, ok := RenderLatex(source, true)
	if !ok || got != "[(1,1,1), (1,1,2), (1,2,5), (1,5,13), (2,5,29), (1,13,34), (1,34,89)]." {
		t.Errorf("got %q ok=%v", got, ok)
	}
	got, ok = RenderLatex("a\\\r\nb", false)
	if !ok || got != "a b" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestRenderLatexDisplayOperatorLimits(t *testing.T) {
	cases := []latexCase{
		{`\sum_{i=0}^n x_i`, " n\n ∑  xᵢ\ni=0", true},
		{`\min_{x\in X} f(x)`, "min f(x)\nx∈X", true},
		{`\operatorname*{arg\,max}_{x\in X} f(x)`, "arg max f(x)\n  x∈X", true},
		{`\int\nolimits_0^1 f(x)\,dx`, "∫₀¹ f(x) dx", true},
		{`\int\limits_0^1 f(x)\,dx`, "1\n∫ f(x) dx\n0", true},
	}
	runLatexCases(t, cases)
}

func TestRenderLatexCentersEvenCaseRowsAroundMiddleBrace(t *testing.T) {
	got, ok := RenderLatex(`f(x) = \begin{cases} x^{2} & x \geq 0 \\ -x & x < 0 \end{cases}`, false)
	if !ok || got != "       ⎧ x² if x ≥ 0\nf(x) = ⎨\n       ⎩ -x if x < 0" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestRenderLatexDisplayFractions(t *testing.T) {
	runLatexCases(t, []latexCase{
		{`x=\frac{-b\pm\sqrt{b^2-4ac}}{2a}`, "    -b±√(b²-4ac)\nx = ────────────\n         2a", true},
		{`\frac{x^2+1}{x-1}`, "x²+1\n────\nx-1", true},
		{"\\frac{1}\n{2}", "1\n─\n2", true},
	})
}

func TestRenderLatexNestedDisplayFractionsLinear(t *testing.T) {
	runLatexCases(t, []latexCase{
		{`\frac{\frac{x^2+1}{x-1}-\frac{2x}{x+1}}{\frac{x}{x^2-1}}`, "(x²+1)/(x-1)-2x/(x+1)\n─────────────────────\n      x/(x²-1)", true},
		{`\lim_{x\to 0}\frac{\frac{\sin x}{x}-1}{\frac{e^x-1}{x}-1}=0`, "     (sin x)/x-1\nlim  ─────────── = 0\nx→0  (eˣ-1)/x-1", true},
		{`\frac{1+\frac{1}{1+\frac{1}{x}}}{1-\frac{1}{1-\frac{1}{x}}}`, "1+1/(1+1/x)\n───────────\n1-1/(1-1/x)", true},
	})
}

func TestRenderLatexLaysOutUnsupportedAndNestedScripts(t *testing.T) {
	// pi v0.86.0: stacked scripts via layout nodes, script fractions stay
	// linear (test renamed from "keeps fractions linear in scripts...").
	got, ok := RenderLatex(`\partial_tU_2(t,0)=Aj_*(1-t)^{-A-1}.\qquad x^{n^2}+x_{i_j}`, true)
	if !ok || got != "                            2\n                    -A-1   n\n∂ₜU₂(t,0) = Aj (1-t)    . x  +x\n              *                i\n                                j" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	got, ok = RenderLatex(`e^{\frac{1}{2}}+\tfrac{1}{2}`, true)
	if !ok || got != "e^(1/2)+1/2" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestRenderLatexUnsupportedCommands(t *testing.T) {
	if _, ok := RenderLatex(`x + \unknown{y}`, false); ok {
		t.Fatal("want unsupported")
	}
}

func TestRenderLatexMalformedGroupsAndEnvironments(t *testing.T) {
	for _, source := range []string{`\frac{1}{x`, "x}", `\begin{matrix}1 & 2`, "x\\"} {
		if _, ok := RenderLatex(source, false); ok {
			t.Errorf("RenderLatex(%q) = supported, want unsupported", source)
		}
	}
}
