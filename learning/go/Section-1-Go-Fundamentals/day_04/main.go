package main

import (
	"fmt"
)

func main() {
	fmt.Println("1. Görev Adresleri Anlamak")
	adresleriAnla()

	fmt.Println("2. Görev Pass by Value vs Pointer")
	degerVsPointer()

	fmt.Println("3. Görev 'new' anahtar kelimesi")
	newKullanimi()

	fmt.Println("4. Görev Nil çökmelerinden kaçınmak")
	nilKontrolu()
}

// 1. GÖREV EKRANI
func adresleriAnla() {
	sayi := 42

	//& ile adres alınır
	ptr := &sayi
	fmt.Printf("Orijinal sayı: %d\n", sayi)
	fmt.Printf("Sayının bellekteki adresi: %p\n", ptr)

	// * ile adresteki değere ulaşıp değiştiriyoruz

	*ptr = 100
	fmt.Printf("Pointer ile değiştirildikten sonra sayı %d\n", sayi)
}

// 2. GÖREV EKRANI
func degerVsPointer() {
	skor := 10

	fmt.Printf("Başlangıç skoru: %d\n", skor)

	//Value ile gönderiyoruz - ORJİNALİ DEĞİŞMEZ
	degerIleDegistir(skor)
	fmt.Printf("degerIleDegistir fonksiyonundan sonra skor: %d\n", skor)

	//Pointer ile gönderiyoruz - ORJİNAL DEĞİŞİR
	pointerIleDegistir(&skor)
	fmt.Printf("pointerIleDegistir fonksiyonundan sonra skor: %d\n", skor)
}

func degerIleDegistir(s int) {
	s = 20
}

func pointerIleDegistir(s *int) {
	*s = 20
}

// 3. GÖREV EKRANI

type Oyuncu struct {
	Isim string
}

func newKullanimi() {
	// Yöntem 1: new ile bellek ayırıyoruz
	//p1 bir pointer (*int) olur ve varsayılan değeri 0 olur
	p1 := new(int)
	*p1 = 50
	fmt.Printf("new() ile oluşturulan p1'in değeri: %d\n", *p1)

	// Yöntem 2: Composite literal kullanarak adres alma
	// p2 bir pointer (*Oyuncu) olur. Daha yaygın bir kullanımdır
	p2 := &Oyuncu{Isim: "AmIHigh?"}

	fmt.Printf("Composite literal ile oluşturulan p2'nin ismi: %s\n", p2.Isim)
}

// 4. GÖREV EKRANI

func nilKontrolu() {
	var bosPointer *int //nil pointer
	// *bosPointer şeklinde olsaydı panic alırdık ve çökerdi.

	if bosPointer != nil {
		fmt.Println("bosPointer nil değil, değeri:", *bosPointer)
	} else {
		fmt.Println("Uyarı: Pointer 'nil' çökme önlendi")
	}
}
