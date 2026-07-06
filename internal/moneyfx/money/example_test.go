package money

import (
	"fmt"
	"math/big"
)

func ExampleMoney_MulRat() {
	down := Money{Amount: 5, Currency: "GBP"}.MulRat(big.NewRat(1, 2))
	up := Money{Amount: 7, Currency: "GBP"}.MulRat(big.NewRat(1, 2))

	fmt.Println(down.Format())
	fmt.Println(up.Format())

	// Output:
	// £0.02
	// £0.04
}

func ExampleMoney_Allocate() {
	parts := (Money{Amount: 5, Currency: "GBP"}).Allocate([]int{1, 1})

	fmt.Println(parts[0].Format(), parts[1].Format())

	// Output:
	// £0.03 £0.02
}

func ExampleParseAmount() {
	amount, _ := ParseAmount("1,234.56", "GBP")

	fmt.Println(amount.Amount, amount.Currency, amount.Format())

	// Output:
	// 123456 GBP £1,234.56
}
