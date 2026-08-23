package fingerprint

import "fmt"

// Merge combines two fingerprints claimed to be the same person into one.
// The blocks concatenate chronologically (all of a's games, then all of b's),
// which is exactly the shape SelfConsistency audits — validate a proposed
// merge with the hygiene package before trusting the result.
//
// Per-race sub-means merge only when every contributing side with games of
// that race also carries its sub-mean (a parsed fingerprint drops sub-means
// below the serialization threshold); otherwise the count is kept and the
// sub-mean omitted. The projection cache does not carry over.
func Merge(a, b *Fingerprint, meta Meta) (*Fingerprint, error) {
	if a.version != b.version {
		return nil, fmt.Errorf("%w: merging v%d with v%d", ErrVersionMismatch, a.version, b.version)
	}
	if a.n == 0 || b.n == 0 {
		return nil, fmt.Errorf("fingerprint: cannot merge an empty fingerprint")
	}

	out := &Fingerprint{
		Meta:       meta,
		version:    a.version,
		dims:       a.dims,
		n:          a.n + b.n,
		mean:       weightedMean(a.mean, a.n, b.mean, b.n),
		raceCounts: map[string]int{},
		raceMeans:  map[string][]float64{},
	}

	for _, src := range []*Fingerprint{a, b} {
		for race, count := range src.raceCounts {
			out.raceCounts[race] += count
		}
	}
	for race := range out.raceCounts {
		na, ma := a.raceCounts[race], a.raceMeans[race]
		nb, mb := b.raceCounts[race], b.raceMeans[race]
		if (na > 0 && ma == nil) || (nb > 0 && mb == nil) {
			continue // a side lost its sub-mean to serialization; count only
		}
		switch {
		case na > 0 && nb > 0:
			out.raceMeans[race] = weightedMean(ma, na, mb, nb)
		case na > 0:
			out.raceMeans[race] = copyVec(ma)
		default:
			out.raceMeans[race] = copyVec(mb)
		}
	}

	for _, src := range []*Fingerprint{a, b} {
		for _, blk := range src.blocks {
			out.blocks = append(out.blocks, block{n: blk.n, mean: copyVec(blk.mean)})
		}
	}
	// Compact back under the block budget, always merging the adjacent pair
	// with the smallest combined count so chronological resolution is lost
	// where it matters least.
	for len(out.blocks) > maxBlocks {
		best, bestN := 0, out.blocks[0].n+out.blocks[1].n
		for i := 1; i < len(out.blocks)-1; i++ {
			if n := out.blocks[i].n + out.blocks[i+1].n; n < bestN {
				best, bestN = i, n
			}
		}
		merged := mergeBlocks(out.blocks[best], out.blocks[best+1])
		out.blocks = append(out.blocks[:best], append([]block{merged}, out.blocks[best+2:]...)...)
	}
	out.blockCap = 1
	for _, blk := range out.blocks {
		if blk.n > out.blockCap {
			out.blockCap = blk.n
		}
	}
	return out, nil
}

func weightedMean(a []float64, na int, b []float64, nb int) []float64 {
	out := make([]float64, len(a))
	wa := float64(na) / float64(na+nb)
	wb := float64(nb) / float64(na+nb)
	for j := range out {
		out[j] = a[j]*wa + b[j]*wb
	}
	return out
}

func copyVec(xs []float64) []float64 {
	out := make([]float64, len(xs))
	copy(out, xs)
	return out
}
