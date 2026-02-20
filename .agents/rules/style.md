---
trigger: always_on
---

Design Language Core Spec v1.0
一、基础方法论
我们采用：

原子化开发模型

4pt Base Grid 系统

低对比克制视觉风格

减法设计原则

禁止渐变
禁止高对比
禁止重投影

二、Layout & Grid System
2.1 Grid
Base Unit：4pt
所有 spacing 必须为 4 的倍数

推荐使用值：
4 / 8 / 12 / 16 / 24 / 32 / 40 / 48

页面最大宽度：1200
页面左右 padding：24

2.2 Spacing 规则
Card 内边距：24
Card 之间间距：16

禁止任意自定义 spacing 数值。

三、Radius System
建立层级半径体系，而不是全部 24。

Micro 组件：12

Card / 容器：24

胶囊按钮 / Input：24

避免全部同级导致视觉塌缩。

四、Border System
Border 宽度统一：1px

颜色使用弱对比灰
不允许高对比描边

五、Shadow & Elevation System
只允许 2 个 Elevation 等级：

Level 1：轻微阴影，用于 Card
Level 2：稍强阴影，用于悬浮层

禁止大面积模糊
禁止超过 2 层 shadow

六、Color System
6.1 基础色板
Primary Neutral：白
Background Neutral：浅灰
Border Neutral：低对比灰
Text Primary：深灰
Text Secondary：中灰

Accent：克莱因蓝

6.2 使用规则
页面主色为白灰体系
克莱因蓝仅用于：

主要按钮

交互高亮

可点击焦点状态

不允许大面积蓝色铺底。

七、Material System
可交互区域使用亚克力玻璃效果：

半透明背景

轻微 blur

低对比边框

禁止重玻璃质感
禁止厚重高光

八、Typography System
8.1 字号 Token
仅允许：

12 / 14 / 16 / 20 / 24 / 32

8.2 语义映射
H1：32
H2：24
H3：20
Body Large：16
Body Small：14
Caption：12

标题行高：1.4
正文行高：1.6

禁止随意使用字号。

九、组件尺寸规范
Button 高度：40
Input 高度：40

Icon 仅允许：16 或 20

十、原子化执行流程
在实现页面时必须按以下顺序输出：

Design Token 定义

原子组件定义（Button / Input / Icon）

分子组件定义（Form / Card）

页面结构布局

最终实现代码

禁止直接生成完整页面代码而跳过结构定义。