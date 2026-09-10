# 2026-09-10：reflect 方法值/函数值身份比较在 Go 移植中的三处误用

日期：2026-09-10 CST（tui_alt_screen.go 修 19:03，utils 包新建 19:14，tui.go/agent_session.go 统一 19:14-19:15，本文档写就 19:20）
状态：✅ isPlainContainerMouse 已修（方案 1）；sameFuncValue/sameStreamFn 审查后统一为 `utils.SameFuncValue`；`-race` 全量回归绿（TestFindTool 失败是另一个 agent 并行 WIP，与本修复无关）

本文件记录同一天、同根因（TS 的「函数/方法身份相等」翻译到 Go 时误用 reflect 指针）的三处代码：一处真 bug（潜伏）、一处审查确认无碍、一处过度声明 + 代码重复。核心区分：**reflect 的「绑定方法值」与「普通函数值」的 `Pointer()` 语义完全不同**。

---

## 核心事实（全部实测锁定，真包 + 真类型）

| 反射对象 | `Pointer()` 返回值 | 能否用于身份比较 |
|---|---|---|
| 绑定方法值 `reflect.ValueOf(x).MethodByName("HandleMouse").Pointer()` | **全部塌成同一个共享 stub**（实测 0x4d8880，Container/TuiBase/Box/SelectList/MouseRegion 全等） | ❌ 完全不能 |
| `reflect.TypeOf(x).MethodByName(...).Func.Pointer()` | promoted 方法返回 reflect 现造的 **wrapper**，各类型指针**各不相同**（Container=0x5e8360、TuiBase(提升)=0x6b8320、Box=0x5a20e0、SelectList=0x5db100、MouseRegion=0x5d9340） | ❌ 提升 vs override 无法区分 |
| 普通函数值 `reflect.ValueOf(plainFunc).Pointer()` | 返回真实代码地址 `funcval.fn`（StreamSimple=0x5f6e80，SDK wrapper 闭包=0x600540） | ✅ 可区分具名函数 / 同一函数值；⚠️ 同字面量闭包不可区分（内联相关） |

`Value.Pointer()` 文档自带警告：「not necessarily enough to identify a single function uniquely」。绑定方法值由 reflect 统一走 invoker stub，普通函数值才解引用 `funcval.fn`。

---

## 一、`isPlainContainerMouse`：reflect 方法值指针全等 → 判断恒真（潜伏 bug，已修）

### 现象

`tui/tui_alt_screen.go` 移植 pi 的 `box.component.handleMouse === Container.prototype.handleMouse`（检测「组件没 override 鼠标处理、用的 Container 默认实现」，layout 分发要跳过它，因为 Container 默认按子索引整宽堆叠，在绝对定位布局下是错的）：

```go
var containerHandleMousePointer = reflect.ValueOf(&Container{}).MethodByName("HandleMouse").Pointer()
func isPlainContainerMouse(component Component) bool {
    method := reflect.ValueOf(component).MethodByName("HandleMouse")
    ...
    return method.Pointer() == containerHandleMousePointer
}
```

实测：绑定方法值指针**对一切实现 MouseHandler 的组件全等**（含 override 的 Box/SelectList），`isPlainContainerMouse` 对任何 MouseHandler 恒返回 true——**没有分辨能力**。原注释「promoted wrapper 跨类型共享」的机理解释是错的（不是提升方法共享，而是所有绑定方法值共享 stub）。

### 为什么当时没爆（两集合不相交）

判断前有 `GetLayoutNode(box.Component) != nil` 门卫。当前实现 LayoutComponent 的只有 `Stack`（VStack/HStack 嵌入）与 `ScrollView`，二者都不 override `HandleMouse`；override 的七个组件（Box/Input/Editor/SelectList/SettingsList/MouseRegion/altImplicitDocument）都**不是** LayoutComponent。恒 true 的 `isPlainContainerMouse` 在该门卫下等价于 `GetLayoutNode != nil`，结果碰巧正确。无任何测试覆盖误判分支。

**潜在 bug**：将来出现「既 override `HandleMouse` 又实现 `LayoutComponent`」的组件会被静默跳过、收不到鼠标事件——不 crash、不报错。

### 修复（方案 1）

删掉 `isPlainContainerMouse` + `containerHandleMousePointer` + `reflect` import，门卫只留 `GetLayoutNode(box.Component) != nil` 并加契约注释：当前所有 LayoutComponent 都依赖 Container 默认分发，若将来有自带鼠标处理的 LayoutComponent，须显式走能力接口（如 `LayoutMouseHandler`）而非反射。行为与修复前完全等价（已验证两集合不相交）。两个 reflect 方案都表达不出「是否 override」，这条已在注释中声明为结构性偏差（§3.4）。

---

## 二、`sameStreamFn`：普通函数值反射可行，当前用法正确（审查确认，未改）

`codingagent/core/agent_session.go` 的 `getSummarizationRequestAuth` 移植 pi 的 `this.agent.streamFunction === streamSimple`。用 `reflect.ValueOf(plainFunc).Pointer()` 比较**普通函数值**——与情况一不同，返回真实代码地址，可用。

逐路径核对与 pi 一致：

| 路径 | pi 的 streamFunction | pig 的 StreamFunction | `=== streamSimple` |
|---|---|---|---|
| SDK 创建 | sdk.ts:314 wrapper 闭包 | sdk.go:436 wrapper 闭包 | false |
| 无 streamFn 默认 | `runtimeOptions.streamFn ?? getDefaultStreamFn()`（agent.ts:222）= streamSimple | 同（agent.go:264-266） | true |

实测 `StreamSimple=0x5f6e80` vs SDK wrapper=0x600540 → 不等，与 pi 的 false 一致。**结论：正确，不用动。** 潜在雷（仅将来使用时的）：若把方法值塞进 `StreamFn` 会塌 stub 误判；同字面量闭包不可靠。当前两侧恒为「裸 `ai.StreamSimple` 或普通闭包」，不会中招。

---

## 三、`sameFuncValue`：注释过度声明 + 内联重复（已修，并统一为 `utils.SameFuncValue`）

### 现象

`tui/tui.go` 的 `RemoveInputListener`/`removeTerminalColorSchemeListener` 用同一 reflect 代码指针比较监听器函数值，替代 pi 的 `Set.delete(listener)`（引用身份）。原注释声称「matching JS Set identity semantics」——**过度声明**：

- JS Set 按**引用身份**（per-instance）删除；代码指针比较**区分不了同一个字面量的两个不同闭包实例**，判等与否**取决于编译器是否内联复制闭包代码**——行为随构建而非确定。
- 已核实全部调用点（tui.go:1275/1302 的 unsubscribe 捕获同一函数值、interactive_mode.go:4400/4418 同一 handler、theme_controller.go:267 同一闭包）都是「同一函数值注册再注销」模式，代码指针必等，**当前行为恒正确**。
- 顺带 `sameFuncValue(nil, nil)` 返回 false（`a != nil && b != nil` 短路），nil 监听器加了删不掉，属退化边角。
- 另一处 `removeTerminalColorSchemeListener` 内联重复了一段相同的 reflect 比较。

### 修复

1. 注释改为诚实表述（同一引用注册/注销模式精确；同字面量闭包不可区分、依赖内联、不可用于新造闭包）。
2. 用户新建顶层 `utils/` 叶子包，落 `utils/same_func_value.go`：`SameFuncValue[T any]`（泛型，`Kind()!=Func` 或任一 nil → false，语义与旧 `sameFuncValue`/`sameStreamFn` 完全一致）。
3. `tui.go` 删本地 `sameFuncValue`，两处调用点直接 `utils.SameFuncValue`；`agent_session.go` 删 `sameStreamFn`，改 `pigutils.SameFuncValue`（`pigutils` 别名避免与已存在的 `codingagent/utils` import 撞名），pi 映射注释移到调用点。
4. 全仓扫描后**第 4 处** `agent/agent.go` 的 `reflectFuncEqual`（`Agent.Subscribe` 的 `Set.delete`，与 tui 监听器同款）也一并改 `utils.SameFuncValue`——顺带修掉它潜在的 nil panic（`reflect.ValueOf(nil).Pointer()` 在零值上直接 panic，原实现没有 nil/kind 守卫；`SameFuncValue` 对非 Func 或 nil 返回 false）。其余三处 `reflect.ValueOf(...).Pointer()`（`sameMapReference`、`sameJSONIdentity`）是 map/slice 引用身份比较，语义不同，不适用。

`utils/` 是 pig 内部共享叶子包（无对应 pi 包，与 `constants/` 同类）。分层的必要性：`tui` 是叶子、`codingagent` 依赖 `tui` 但反向不行，故共享 helper 必须放一个两者都能 import 的叶子位。

### 验证

`gofmt -l` 干净；`go vet ./utils/ ./tui/ ./codingagent/core/` 干净；`go test ./utils/ ./tui/ ./codingagent/core/ -count=1` 全绿；全仓 `-race` 回归绿。

---

## 复现方法备忘

方法值塌 stub 的实测探针（已删）：

```go
// tui 包内临时测试
reflect.ValueOf(&Box{}).MethodByName("HandleMouse").Pointer()
// == reflect.ValueOf(&Container{}).MethodByName("HandleMouse").Pointer() == 0x4d8880
// 任何实现 MouseHandler 的组件都得到同一地址
```

普通函数值探针（已删）：

```go
reflect.ValueOf(ai.StreamSimple).Pointer()                 // 0x5f6e80
reflect.ValueOf(sdkWrapperClosure).Pointer()               // 0x600540
```
