package features

import (
	"fmt"
	"math"
	"sort"

	"github.com/icza/screp/rep/repcmd"
	"github.com/icza/screp/rep/repcore"
)

// Command classes for bigrams and rhythm features.
const (
	classSelect       = 0
	classSelectMod    = 1 // select add/remove
	classHKAssign     = 2
	classHKSelect     = 3
	classRightClick   = 4
	classTargeted     = 5
	classBuild        = 6
	classProduce      = 7 // train / morph / tech / upgrade
	classQueueableEtc = 8
	classOther        = 9
	numClasses        = 10
)

// coreClasses are the classes with enough volume for bigram-conditioned
// inter-command-interval medians: select, hkselect, rightclick, targeted, train.
var coreClasses = []int{classSelect, classHKSelect, classRightClick, classTargeted, classProduce}

var typeBuckets = []string{
	"select", "select_add", "select_remove", "hotkey_assign", "hotkey_select",
	"rightclick", "targeted_order", "build", "train", "unit_morph",
	"building_morph", "tech", "upgrade", "stop_hold", "return_cargo",
	"cancel", "burrow_siege_cloak", "unload", "liftoff_land", "other",
}

// Indices into typeCounts, in typeBuckets order.
const (
	bucketSelect = iota
	bucketSelectAdd
	bucketSelectRemove
	bucketHotkeyAssign
	bucketHotkeySelect
	bucketRightclick
	bucketTargetedOrder
	bucketBuild
	bucketTrain
	bucketUnitMorph
	bucketBuildingMorph
	bucketTech
	bucketUpgrade
	bucketStopHold
	bucketReturnCargo
	bucketCancel
	bucketBurrowSiegeCloak
	bucketUnload
	bucketLiftoffLand
	bucketOther
	numTypeBuckets
)

var (
	a2sBins  = []float64{6, 12, 24, 48, 120, math.Inf(1)}
	distBins = []float64{16, 64, 160, 320, 640, 1280, math.Inf(1)}
	iciBins  = []float64{1, 2, 3, 4, 6, 8, 12, 16, 24, 36, 48, 72, 120, 240, math.Inf(1)}
)

func featureNamesV3() []string {
	names := []string{"apm", "eapm", "redundancy", "apm_early", "apm_mid", "apm_late"}
	for _, t := range typeBuckets {
		names = append(names, "frac_"+t)
	}
	for i := 0; i < 10; i++ {
		names = append(names, fmt.Sprintf("hk_assign_g%d", i))
	}
	for i := 0; i < 10; i++ {
		names = append(names, fmt.Sprintf("hk_select_g%d", i))
	}
	names = append(names,
		"hk_per_min", "hk_assigns_per_min", "hk_sel_assign_ratio", "hk_double_tap_rate",
		"sel_size_mean", "sel_size_p90",
		"queued_frac",
		"ici_p10", "ici_p25", "ici_p50", "ici_p75", "ici_p90", "ici_mean",
		"ici_frac_0", "ici_frac_le2", "ici_frac_ge24",
		"dist_p50", "dist_p90", "dist_frac_far",
		"pings_per_min", "chats_per_min",
	)
	for i := 0; i < numClasses; i++ {
		for j := 0; j < numClasses; j++ {
			names = append(names, fmt.Sprintf("bigram_%d_%d", i, j))
		}
	}
	for i := 0; i < 10; i++ {
		for j := 0; j < 10; j++ {
			names = append(names, fmt.Sprintf("hktrans_%d_%d", i, j))
		}
	}
	names = append(names, "a2s_lat_p25", "a2s_lat_p50", "a2s_lat_p75", "dbltap_gap_med")
	for b := 0; b < len(iciBins); b++ {
		names = append(names, fmt.Sprintf("ici_hist_%d", b))
	}
	for c := 0; c < numClasses; c++ {
		names = append(names, fmt.Sprintf("preici_med_c%d", c))
	}
	names = append(names, "burst_run_mean")
	for _, i := range coreClasses {
		for _, j := range coreClasses {
			names = append(names, fmt.Sprintf("bici_%d_%d", i, j))
		}
	}
	for b := 0; b < 13; b++ {
		names = append(names, fmt.Sprintf("dblgap_h%d", b))
	}
	for b := 0; b < len(a2sBins); b++ {
		names = append(names, fmt.Sprintf("a2s_h%d", b))
	}
	for g := 0; g < 10; g++ {
		names = append(names, fmt.Sprintf("firstassign_g%d", g))
	}
	names = append(names, "ici_mode", "burst_cadence")
	for b := 0; b < len(distBins); b++ {
		names = append(names, fmt.Sprintf("dist_h%d", b))
	}
	return names
}

type accumulator struct {
	name, race string

	frames []float64

	typeCounts [numTypeBuckets]float64
	total      float64

	hkAssignGroup [10]float64
	hkSelectGroup [10]float64
	hkAssigns     float64
	hkSelects     float64
	hkDoubleTaps  float64
	lastHKSelGrp  int
	lastHKSelFrm  float64

	selectSizes []float64

	queueable    float64
	queued       float64
	posDists     []float64
	lastPosX     float64
	lastPosY     float64
	hasLastPos   bool
	minimapPings float64
	chats        float64

	earlyCmds, midCmds, lateCmds float64

	effective float64

	bigram        [numClasses][numClasses]float64
	biICI         [numClasses * numClasses][]float64
	prevClass     int
	firstAssigns  []int
	iciFineBins   [24]float64
	hkSelTrans    [10][10]float64
	pendingAssign [10]float64
	pendingSet    [10]bool
	a2sLatencies  []float64
	dblTapGaps    []float64
	classICI      [numClasses][]float64
	burstRuns     []float64
	curRun        float64
}

// newAccumulator sizes the largest per-command buffers up front from the
// player's known command count; slice regrowth otherwise dominates extraction
// time via GC pressure.
func newAccumulator(cmdCount int) *accumulator {
	a := &accumulator{
		lastHKSelGrp: -1,
		prevClass:    -1,
		frames:       make([]float64, 0, cmdCount),
		selectSizes:  make([]float64, 0, cmdCount/2),
		posDists:     make([]float64, 0, cmdCount/2),
	}
	for c := range a.classICI {
		a.classICI[c] = make([]float64, 0, cmdCount/4)
	}
	return a
}

func (a *accumulator) addPos(x, y float64) {
	if a.hasLastPos {
		dx, dy := x-a.lastPosX, y-a.lastPosY
		a.posDists = append(a.posDists, math.Hypot(dx, dy))
	}
	a.lastPosX, a.lastPosY, a.hasLastPos = x, y, true
}

// add feeds one command into the accumulator. Commands must arrive in replay
// (frame) order.
func (a *accumulator) add(cmd repcmd.Cmd) {
	base := cmd.BaseCmd()
	f := float64(base.Frame)
	hasPrev := len(a.frames) > 0
	prevF := 0.0
	if hasPrev {
		prevF = a.frames[len(a.frames)-1]
	}
	a.frames = append(a.frames, f)
	a.total++
	cls := classOther
	if base.IneffKind == repcore.IneffKindEffective {
		a.effective++
	}
	switch {
	case f < 3*framesPerMin:
		a.earlyCmds++
	case f < 8*framesPerMin:
		a.midCmds++
	default:
		a.lateCmds++
	}

	switch c := cmd.(type) {
	case *repcmd.SelectCmd:
		// 1.21+ replays use the 121 select type IDs (0x63–0x65); handling
		// only the classic IDs silently zeroes all select features.
		switch base.Type.ID {
		case repcmd.TypeIDSelect, repcmd.TypeIDSelect121:
			a.typeCounts[bucketSelect]++
			a.selectSizes = append(a.selectSizes, float64(len(c.UnitTags)))
			cls = classSelect
		case repcmd.TypeIDSelectAdd, repcmd.TypeIDSelectAdd121:
			a.typeCounts[bucketSelectAdd]++
			cls = classSelectMod
		default:
			a.typeCounts[bucketSelectRemove]++
			cls = classSelectMod
		}
	case *repcmd.HotkeyCmd:
		g := int(c.Group)
		if g > 9 {
			g = 9
		}
		if c.HotkeyType.ID == repcmd.HotkeyTypeIDAssign || c.HotkeyType.ID == repcmd.HotkeyTypeIDAdd {
			a.typeCounts[bucketHotkeyAssign]++
			a.hkAssigns++
			a.hkAssignGroup[g]++
			a.pendingAssign[g], a.pendingSet[g] = f, true
			cls = classHKAssign
		} else {
			a.typeCounts[bucketHotkeySelect]++
			a.hkSelects++
			a.hkSelectGroup[g]++
			if a.lastHKSelGrp == g && f-a.lastHKSelFrm <= 8 {
				a.hkDoubleTaps++
			}
			if a.lastHKSelGrp == g && f-a.lastHKSelFrm <= 12 {
				a.dblTapGaps = append(a.dblTapGaps, f-a.lastHKSelFrm)
			}
			if a.lastHKSelGrp >= 0 {
				a.hkSelTrans[a.lastHKSelGrp][g]++
			}
			if a.pendingSet[g] {
				a.a2sLatencies = append(a.a2sLatencies, f-a.pendingAssign[g])
				a.pendingSet[g] = false
			}
			a.lastHKSelGrp = g
			a.lastHKSelFrm = f
			cls = classHKSelect
		}
	case *repcmd.RightClickCmd:
		a.typeCounts[bucketRightclick]++
		cls = classRightClick
		a.queueable++
		if c.Queued {
			a.queued++
		}
		a.addPos(float64(c.Pos.X), float64(c.Pos.Y))
	case *repcmd.TargetedOrderCmd:
		a.typeCounts[bucketTargetedOrder]++
		cls = classTargeted
		a.queueable++
		if c.Queued {
			a.queued++
		}
		a.addPos(float64(c.Pos.X), float64(c.Pos.Y))
	case *repcmd.BuildCmd:
		a.typeCounts[bucketBuild]++
		a.addPos(float64(c.Pos.X), float64(c.Pos.Y))
		cls = classBuild
	case *repcmd.TrainCmd:
		if base.Type.ID == repcmd.TypeIDTrain {
			a.typeCounts[bucketTrain]++
		} else {
			a.typeCounts[bucketUnitMorph]++
		}
		cls = classProduce
	case *repcmd.BuildingMorphCmd:
		a.typeCounts[bucketBuildingMorph]++
		cls = classProduce
	case *repcmd.TechCmd:
		a.typeCounts[bucketTech]++
		cls = classProduce
	case *repcmd.UpgradeCmd:
		a.typeCounts[bucketUpgrade]++
		cls = classProduce
	case *repcmd.QueueableCmd:
		cls = classQueueableEtc
		a.queueable++
		if c.Queued {
			a.queued++
		}
		switch base.Type.ID {
		case repcmd.TypeIDStop, repcmd.TypeIDHoldPosition:
			a.typeCounts[bucketStopHold]++
		case repcmd.TypeIDReturnCargo:
			a.typeCounts[bucketReturnCargo]++
		case repcmd.TypeIDBurrow, repcmd.TypeIDUnburrow, repcmd.TypeIDSiege, repcmd.TypeIDUnsiege, repcmd.TypeIDCloack, repcmd.TypeIDDecloack:
			a.typeCounts[bucketBurrowSiegeCloak]++
		default:
			a.typeCounts[bucketOther]++
		}
	case *repcmd.MinimapPingCmd:
		a.minimapPings++
		a.typeCounts[bucketOther]++
	case *repcmd.ChatCmd:
		a.chats++
		a.typeCounts[bucketOther]++
	case *repcmd.UnloadCmd:
		a.typeCounts[bucketUnload]++
	case *repcmd.LiftOffCmd:
		a.typeCounts[bucketLiftoffLand]++
		a.addPos(float64(c.Pos.X), float64(c.Pos.Y))
	case *repcmd.LandCmd:
		a.typeCounts[bucketLiftoffLand]++
		a.addPos(float64(c.Pos.X), float64(c.Pos.Y))
	default:
		switch base.Type.ID {
		case repcmd.TypeIDCancelBuild, repcmd.TypeIDCancelMorph, repcmd.TypeIDCancelTrain, repcmd.TypeIDCancelNuke, repcmd.TypeIDCancelTech, repcmd.TypeIDCancelUpgrade, repcmd.TypeIDCancelAddon:
			a.typeCounts[bucketCancel]++
		default:
			a.typeCounts[bucketOther]++
		}
	}

	if a.prevClass >= 0 {
		a.bigram[a.prevClass][cls]++
		if hasPrev {
			a.biICI[a.prevClass*10+cls] = append(a.biICI[a.prevClass*10+cls], f-prevF)
		}
	}
	a.prevClass = cls
	if cls == classHKAssign && len(a.firstAssigns) < 5 {
		if hc, ok := cmd.(*repcmd.HotkeyCmd); ok {
			g := int(hc.Group)
			if g > 9 {
				g = 9
			}
			a.firstAssigns = append(a.firstAssigns, g)
		}
	}
	if hasPrev {
		d := f - prevF
		a.classICI[cls] = append(a.classICI[cls], d)
		if d < 24 {
			a.iciFineBins[int(d)]++
		}
		if d <= 2 {
			a.curRun++
		} else {
			if a.curRun > 0 {
				a.burstRuns = append(a.burstRuns, a.curRun+1)
			}
			a.curRun = 0
		}
	}
}

// features flattens the accumulated state into the v3 vector. It must only be
// called once, after all commands were added, and requires at least one
// accumulated command.
func (a *accumulator) features() []float64 {
	lastFrame := a.frames[len(a.frames)-1]
	activeMin := lastFrame / framesPerMin
	if activeMin < 0.5 {
		activeMin = 0.5
	}
	apm := a.total / activeMin
	eapm := a.effective / activeMin
	red := 0.0
	if a.total > 0 {
		red = 1 - a.effective/a.total
	}

	earlyMin := math.Min(activeMin, 3)
	midMin := math.Max(0.001, math.Min(activeMin, 8)-3)
	lateMin := math.Max(0.001, activeMin-8)
	apmEarly := a.earlyCmds / earlyMin
	apmMid := 0.0
	if activeMin > 3 {
		apmMid = a.midCmds / midMin
	}
	apmLate := 0.0
	if activeMin > 8 {
		apmLate = a.lateCmds / lateMin
	}

	fs := []float64{apm, eapm, red, apmEarly, apmMid, apmLate}
	for b := 0; b < numTypeBuckets; b++ {
		fs = append(fs, a.typeCounts[b]/a.total)
	}
	for i := 0; i < 10; i++ {
		v := 0.0
		if a.hkAssigns > 0 {
			v = a.hkAssignGroup[i] / a.hkAssigns
		}
		fs = append(fs, v)
	}
	for i := 0; i < 10; i++ {
		v := 0.0
		if a.hkSelects > 0 {
			v = a.hkSelectGroup[i] / a.hkSelects
		}
		fs = append(fs, v)
	}
	selAssignRatio := 0.0
	if a.hkAssigns > 0 {
		selAssignRatio = a.hkSelects / a.hkAssigns
	}
	dblRate := 0.0
	if a.hkSelects > 0 {
		dblRate = a.hkDoubleTaps / a.hkSelects
	}
	fs = append(fs, (a.hkAssigns+a.hkSelects)/activeMin, a.hkAssigns/activeMin, selAssignRatio, dblRate)

	sort.Float64s(a.selectSizes)
	fs = append(fs, mean(a.selectSizes), pct(a.selectSizes, 0.9))

	qf := 0.0
	if a.queueable > 0 {
		qf = a.queued / a.queueable
	}
	fs = append(fs, qf)

	icis := make([]float64, 0, len(a.frames)-1)
	n0, nle2, nge24 := 0.0, 0.0, 0.0
	for i := 1; i < len(a.frames); i++ {
		d := a.frames[i] - a.frames[i-1]
		icis = append(icis, d)
		if d == 0 {
			n0++
		}
		if d <= 2 {
			nle2++
		}
		if d >= 24 {
			nge24++
		}
	}
	sort.Float64s(icis)
	nn := float64(len(icis))
	if nn == 0 {
		nn = 1
	}
	fs = append(fs, pct(icis, 0.1), pct(icis, 0.25), pct(icis, 0.5), pct(icis, 0.75), pct(icis, 0.9), mean(icis),
		n0/nn, nle2/nn, nge24/nn)

	sort.Float64s(a.posDists)
	far := 0.0
	for _, d := range a.posDists {
		if d > 512 {
			far++
		}
	}
	nd := float64(len(a.posDists))
	if nd == 0 {
		nd = 1
	}
	fs = append(fs, pct(a.posDists, 0.5), pct(a.posDists, 0.9), far/nd)

	fs = append(fs, a.minimapPings/activeMin, a.chats/activeMin)

	bigramTotal := 0.0
	for i := 0; i < numClasses; i++ {
		for j := 0; j < numClasses; j++ {
			bigramTotal += a.bigram[i][j]
		}
	}
	if bigramTotal == 0 {
		bigramTotal = 1
	}
	for i := 0; i < numClasses; i++ {
		for j := 0; j < numClasses; j++ {
			fs = append(fs, a.bigram[i][j]/bigramTotal)
		}
	}
	transTotal := 0.0
	for i := 0; i < 10; i++ {
		for j := 0; j < 10; j++ {
			transTotal += a.hkSelTrans[i][j]
		}
	}
	if transTotal == 0 {
		transTotal = 1
	}
	for i := 0; i < 10; i++ {
		for j := 0; j < 10; j++ {
			fs = append(fs, a.hkSelTrans[i][j]/transTotal)
		}
	}
	sort.Float64s(a.a2sLatencies)
	fs = append(fs, pct(a.a2sLatencies, 0.25), pct(a.a2sLatencies, 0.5), pct(a.a2sLatencies, 0.75))
	sort.Float64s(a.dblTapGaps)
	fs = append(fs, pct(a.dblTapGaps, 0.5))

	histCounts := make([]float64, len(iciBins))
	for _, d := range icis {
		for b, edge := range iciBins {
			if d < edge {
				histCounts[b]++
				break
			}
		}
	}
	for _, h := range histCounts {
		fs = append(fs, h/nn)
	}
	for c := 0; c < numClasses; c++ {
		sort.Float64s(a.classICI[c])
		fs = append(fs, pct(a.classICI[c], 0.5))
	}
	fs = append(fs, mean(a.burstRuns))

	for _, i := range coreClasses {
		for _, j := range coreClasses {
			xs := a.biICI[i*10+j]
			sort.Float64s(xs)
			fs = append(fs, pct(xs, 0.5))
		}
	}
	dg := make([]float64, 13)
	for _, g := range a.dblTapGaps {
		b := int(g)
		if b > 12 {
			b = 12
		}
		dg[b]++
	}
	dgn := math.Max(float64(len(a.dblTapGaps)), 1)
	for _, v := range dg {
		fs = append(fs, v/dgn)
	}
	ah := make([]float64, len(a2sBins))
	for _, l := range a.a2sLatencies {
		for b, edge := range a2sBins {
			if l < edge {
				ah[b]++
				break
			}
		}
	}
	ahn := math.Max(float64(len(a.a2sLatencies)), 1)
	for _, v := range ah {
		fs = append(fs, v/ahn)
	}
	fa := make([]float64, 10)
	for _, g := range a.firstAssigns {
		fa[g]++
	}
	fan := math.Max(float64(len(a.firstAssigns)), 1)
	for _, v := range fa {
		fs = append(fs, v/fan)
	}
	modeBin, modeCount := 0.0, -1.0
	for b, c := range a.iciFineBins {
		if b >= 1 && c > modeCount {
			modeCount = c
			modeBin = float64(b)
		}
	}
	fs = append(fs, modeBin)
	var burstICIs []float64
	for i := 1; i < len(a.frames); i++ {
		d := a.frames[i] - a.frames[i-1]
		if d >= 1 && d <= 4 {
			burstICIs = append(burstICIs, d)
		}
	}
	sort.Float64s(burstICIs)
	fs = append(fs, pct(burstICIs, 0.5))
	dh := make([]float64, len(distBins))
	for _, dd := range a.posDists {
		for b, edge := range distBins {
			if dd < edge {
				dh[b]++
				break
			}
		}
	}
	dhn := math.Max(float64(len(a.posDists)), 1)
	for _, v := range dh {
		fs = append(fs, v/dhn)
	}
	return fs
}

// pct linearly interpolates the p-th percentile of an already-sorted slice.
func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}
