package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/sul/streamflow/internal/fraud"
)

func main() {
	fmt.Println("🏦 StreamFlow Banking Transaction Simulator")
	fmt.Println("==========================================\n")

	baseURL := "http://localhost:8083"
	
	// Проверяем доступность API
	fmt.Println("Checking Banking API...")
	time.Sleep(1 * time.Second)

	// Сценарий 1: Нормальные транзакции
	fmt.Println("\n📊 Scenario 1: Normal Transactions")
	runNormalTransactions(baseURL, 10)

	time.Sleep(2 * time.Second)

	// Сценарий 2: Velocity Attack (много транзакций за короткое время)
	fmt.Println("\n⚡ Scenario 2: Velocity Attack (FRAUD)")
	runVelocityAttack(baseURL)

	time.Sleep(2 * time.Second)

	// Сценарий 3: Превышение лимитов
	fmt.Println("\n💳 Scenario 3: Limit Exceeded")
	runLimitExceeded(baseURL)

	time.Sleep(2 * time.Second)

	// Сценарий 4: Подозрительная локация
	fmt.Println("\n🌍 Scenario 4: Location Anomaly (FRAUD)")
	runLocationAnomaly(baseURL)

	time.Sleep(2 * time.Second)

	// Сценарий 5: Высокорисковый мерчант
	fmt.Println("\n🎰 Scenario 5: High-Risk Merchant")
	runHighRiskMerchant(baseURL)

	// Статистика
	fmt.Println("\n📈 Getting Fraud Statistics...")
	getFraudStats(baseURL)

	fmt.Println("\n✅ Simulation completed!")
}

func runNormalTransactions(baseURL string, count int) {
	cardNumber := "1234"
	
	for i := 0; i < count; i++ {
		tx := generateTransaction(cardNumber, 1000+rand.Float64()*5000, "RU", "Moscow")
		result := sendTransaction(baseURL, tx)
		
		if result.Status == "approved" {
			fmt.Printf("  ✅ Transaction %d: %s %.2f RUB - APPROVED\n", i+1, tx.MerchantName, tx.Amount)
		} else {
			fmt.Printf("  ❌ Transaction %d: %s - %s\n", i+1, result.Status, result.Reason)
		}
		
		time.Sleep(200 * time.Millisecond)
	}
}

func runVelocityAttack(baseURL string) {
	cardNumber := "5678"
	
	fmt.Println("  Simulating 6 transactions in 1 minute...")
	
	for i := 0; i < 6; i++ {
		tx := generateTransaction(cardNumber, 1000, "RU", "Moscow")
		result := sendTransaction(baseURL, tx)
		
		if result.Status == "approved" {
			fmt.Printf("  ✅ Transaction %d: APPROVED\n", i+1)
		} else if result.Status == "fraud_detected" {
			fmt.Printf("  🚨 Transaction %d: FRAUD DETECTED - %s (Confidence: %.2f)\n", i+1, result.Reason, result.Confidence)
			fmt.Printf("     Rule: %s, Action: %s\n", result.TriggeredRule, result.Action)
			break
		}
		
		time.Sleep(100 * time.Millisecond)
	}
}

func runLimitExceeded(baseURL string) {
	cardNumber := "9999"
	
	// Пытаемся сделать транзакцию больше лимита
	tx := generateTransaction(cardNumber, 150000, "RU", "Moscow") // Больше дневного лимита
	result := sendTransaction(baseURL, tx)
	
	if result.Status == "declined" {
		fmt.Printf("  ⛔ Transaction DECLINED - %s\n", result.Reason)
		fmt.Printf("     Limit: %.2f, Spent: %.2f\n", result.Limit, result.Spent)
	}
}

func runLocationAnomaly(baseURL string) {
	cardNumber := "3333"
	
	// Транзакция из России
	tx1 := generateTransaction(cardNumber, 2000, "RU", "Moscow")
	result1 := sendTransaction(baseURL, tx1)
	fmt.Printf("  ✅ Transaction 1 (Russia): %s\n", result1.Status)
	
	time.Sleep(500 * time.Millisecond)
	
	// Через 1 секунду транзакция из США (физически невозможно)
	tx2 := generateTransaction(cardNumber, 3000, "US", "New York")
	result2 := sendTransaction(baseURL, tx2)
	
	if result2.Status == "fraud_detected" {
		fmt.Printf("  🚨 Transaction 2 (USA): FRAUD DETECTED - %s\n", result2.Reason)
		fmt.Printf("     Details: %v\n", result2.Details)
	}
}

func runHighRiskMerchant(baseURL string) {
	cardNumber := "7777"
	
	tx := generateTransaction(cardNumber, 5000, "RU", "Moscow")
	tx.MerchantMCC = "7995" // Gambling
	tx.MerchantName = "OnlineCasino.com"
	
	result := sendTransaction(baseURL, tx)
	
	if result.Status == "fraud_detected" {
		fmt.Printf("  ⚠️  High-Risk Merchant: %s\n", result.Reason)
		fmt.Printf("     Action: %s (for review)\n", result.Action)
	}
}

func getFraudStats(baseURL string) {
	resp, err := http.Get(baseURL + "/api/v1/banking/fraud/stats")
	if err != nil {
		fmt.Printf("  ❌ Failed to get stats: %v\n", err)
		return
	}
	defer resp.Body.Close()

	var stats map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&stats)

	fmt.Printf("  Total Checked: %.0f\n", stats["total_checked"])
	fmt.Printf("  Fraud Detected: %.0f\n", stats["fraud_detected"])
	fmt.Printf("  Cards Blocked: %.0f\n", stats["cards_blocked"])
	fmt.Printf("  Fraud Rate: %.2f%%\n", stats["fraud_rate"])
}

func generateTransaction(cardNumber string, amount float64, country, city string) *fraud.BankTransaction {
	return &fraud.BankTransaction{
		TransactionID: fmt.Sprintf("tx_%d", time.Now().UnixNano()),
		CardNumber:    cardNumber,
		Amount:        amount,
		Currency:      "RUB",
		MerchantID:    fmt.Sprintf("merch_%d", rand.Intn(1000)),
		MerchantName:  getMerchantName(),
		MerchantMCC:   "5411", // Grocery stores
		Timestamp:     time.Now(),
		IPAddress:     generateIP(),
		DeviceID:      fmt.Sprintf("device_%s", cardNumber),
		Location: fraud.GeoLocation{
			Country: country,
			City:    city,
			Lat:     55.7558,
			Lon:     37.6173,
		},
		CardType:   "debit",
		AccountID:  fmt.Sprintf("acc_%s", cardNumber),
		UserID:     fmt.Sprintf("user_%s", cardNumber),
	}
}

func getMerchantName() string {
	merchants := []string{
		"Продукты24",
		"Магазин У Дома",
		"Пятерочка",
		"Магнит",
		"OZON",
		"Wildberries",
		"Яндекс.Маркет",
	}
	return merchants[rand.Intn(len(merchants))]
}

func generateIP() string {
	return fmt.Sprintf("185.%d.%d.%d", rand.Intn(256), rand.Intn(256), rand.Intn(256))
}

func sendTransaction(baseURL string, tx *fraud.BankTransaction) TransactionResult {
	jsonData, _ := json.Marshal(tx)
	
	resp, err := http.Post(
		baseURL+"/api/v1/banking/transaction",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return TransactionResult{Status: "error", Reason: err.Error()}
	}
	defer resp.Body.Close()

	var result TransactionResult
	json.NewDecoder(resp.Body).Decode(&result)
	
	return result
}

type TransactionResult struct {
	Status         string                 `json:"status"`
	Reason         string                 `json:"reason"`
	Confidence     float64                `json:"confidence"`
	Action         string                 `json:"action"`
	TriggeredRule  string                 `json:"triggered_rule"`
	Details        map[string]interface{} `json:"details"`
	Limit          float64                `json:"limit"`
	Spent          float64                `json:"spent"`
	TransactionID  string                 `json:"transaction_id"`
	Amount         float64                `json:"amount"`
	RemainingLimit float64                `json:"remaining_limit"`
}

