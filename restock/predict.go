package restock

import "slices"

// Predict memperkirakan lama pemakaian satu unit dari riwayat.
//
// Dari maksimal 5 siklus terakhir dicari mediannya, lalu siklus yang kurang
// dari separuh atau lebih dari dua kali median dibuang. Siklus seperti itu
// biasanya karena lupa lapor "habis", dan satu kesalahan begitu tidak boleh
// merusak perkiraan. Hasilnya median dari 3 siklus terbaru yang tersisa.
//
// Tanpa riwayat sama sekali, guess dipakai kalau ada.
func Predict(cycles []int, guess int) (int, bool) {
	if len(cycles) == 0 {
		return guess, guess > 0
	}
	recent := lastN(cycles, 5)
	m := median(recent)

	var kept []int
	for _, c := range recent {
		if 2*c >= m && c <= 2*m {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 {
		kept = recent
	}
	return median(lastN(kept, 3)), true
}

func lastN(s []int, n int) []int {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// median dibulatkan ke hari terdekat kalau jumlah datanya genap.
func median(s []int) int {
	sorted := slices.Clone(s)
	slices.Sort(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2] + 1) / 2
}
