package collector

// RateLimiter controla la velocidad de ejecución de operaciones
type RateLimiter struct {
	maxConcurrent int
	semaphore     chan struct{}
}

// NewRateLimiter crea un nuevo rate limiter
func NewRateLimiter(maxConcurrent int) *RateLimiter {
	return &RateLimiter{
		maxConcurrent: maxConcurrent,
		semaphore:     make(chan struct{}, maxConcurrent),
	}
}

// Acquire intenta adquirir un slot
func (rl *RateLimiter) Acquire() {
	rl.semaphore <- struct{}{}
}

// Release libera un slot
func (rl *RateLimiter) Release() {
	<-rl.semaphore
}

// Wait espera a que haya un slot disponible (alias para Acquire)
func (rl *RateLimiter) Wait() {
	rl.Acquire()
}
