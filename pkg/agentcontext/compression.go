package agentcontext

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// CompressionMethod indicates how content was compressed
type CompressionMethod string

const (
	CompressionMethodLLM        CompressionMethod = "llm"
	CompressionMethodExtractive CompressionMethod = "extractive"
	CompressionMethodTFIDF      CompressionMethod = "tfidf"
	CompressionMethodTruncate   CompressionMethod = "truncate"
)

// CompressionResult contains the compressed content and metadata
type CompressionResult struct {
	Summary       string            `json:"summary"`
	Keywords      []string          `json:"keywords,omitempty"`
	Method        CompressionMethod `json:"method"`
	OriginalLen   int               `json:"original_len"`
	CompressedLen int               `json:"compressed_len"`
	Ratio         float64           `json:"ratio"`
}

// FallbackCompressor provides compression when LLM is unavailable
type FallbackCompressor struct {
	// Stopwords to exclude from TF-IDF
	stopwords map[string]bool
}

// NewFallbackCompressor creates a new fallback compressor
func NewFallbackCompressor() *FallbackCompressor {
	return &FallbackCompressor{
		stopwords: defaultStopwords(),
	}
}

// Compress compresses content using extractive summarization and TF-IDF keywords
func (fc *FallbackCompressor) Compress(content string, targetRatio float64) CompressionResult {
	if targetRatio <= 0 || targetRatio > 1 {
		targetRatio = 0.5 // Default to 50%
	}

	result := CompressionResult{
		OriginalLen: len(content),
	}

	// Split into sentences
	sentences := splitSentences(content)
	if len(sentences) == 0 {
		result.Summary = content
		result.Method = CompressionMethodTruncate
		result.CompressedLen = len(content)
		result.Ratio = 1.0
		return result
	}

	// Calculate TF-IDF scores for keywords
	keywords := fc.extractKeywords(content, 10)
	result.Keywords = keywords

	// Score sentences by keyword density and position
	scoredSentences := fc.scoreSentences(sentences, keywords)

	// Select top sentences to meet target ratio
	targetLen := int(float64(len(content)) * targetRatio)
	selected := fc.selectSentences(scoredSentences, targetLen)

	if len(selected) == 0 {
		// Fall back to simple truncation
		result.Summary = truncateText(content, targetLen)
		result.Method = CompressionMethodTruncate
	} else {
		result.Summary = strings.Join(selected, " ")
		result.Method = CompressionMethodExtractive
	}

	result.CompressedLen = len(result.Summary)
	if result.OriginalLen > 0 {
		result.Ratio = float64(result.CompressedLen) / float64(result.OriginalLen)
	}

	return result
}

// ExtractKeywords extracts the most important keywords using TF-IDF
func (fc *FallbackCompressor) ExtractKeywords(content string, maxKeywords int) []string {
	return fc.extractKeywords(content, maxKeywords)
}

// scoredSentence holds a sentence with its importance score
type scoredSentence struct {
	text     string
	score    float64
	position int
}

// splitSentences splits text into sentences
func splitSentences(text string) []string {
	// Simple sentence splitting on . ! ? followed by space or end
	re := regexp.MustCompile(`[.!?]+\s+`)
	parts := re.Split(text, -1)

	var sentences []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) > 10 { // Skip very short fragments
			sentences = append(sentences, p)
		}
	}
	return sentences
}

// extractKeywords extracts keywords using TF-IDF scoring
func (fc *FallbackCompressor) extractKeywords(content string, maxKeywords int) []string {
	// Tokenize
	words := tokenize(content)
	if len(words) == 0 {
		return nil
	}

	// Calculate term frequency
	tf := make(map[string]float64)
	for _, w := range words {
		w = strings.ToLower(w)
		if fc.stopwords[w] || len(w) < 3 {
			continue
		}
		tf[w]++
	}

	// Normalize TF
	maxTF := 0.0
	for _, count := range tf {
		if count > maxTF {
			maxTF = count
		}
	}
	if maxTF > 0 {
		for word := range tf {
			tf[word] /= maxTF
		}
	}

	// Simple IDF approximation (penalize very common terms)
	// Using log(total_words / term_count) approximation
	totalWords := float64(len(words))
	tfidf := make(map[string]float64)
	for word, tfScore := range tf {
		count := 0.0
		for _, w := range words {
			if strings.ToLower(w) == word {
				count++
			}
		}
		idf := math.Log(totalWords / (1 + count))
		tfidf[word] = tfScore * idf
	}

	// Sort by TF-IDF score
	type scoredWord struct {
		word  string
		score float64
	}
	var scored []scoredWord
	for word, score := range tfidf {
		scored = append(scored, scoredWord{word, score})
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Return top keywords
	result := make([]string, 0, maxKeywords)
	for i := 0; i < len(scored) && i < maxKeywords; i++ {
		result = append(result, scored[i].word)
	}
	return result
}

// scoreSentences scores sentences based on keyword density and position
func (fc *FallbackCompressor) scoreSentences(sentences []string, keywords []string) []scoredSentence {
	keywordSet := make(map[string]bool)
	for _, k := range keywords {
		keywordSet[strings.ToLower(k)] = true
	}

	scored := make([]scoredSentence, len(sentences))
	for i, sent := range sentences {
		words := tokenize(sent)
		keywordCount := 0
		for _, w := range words {
			if keywordSet[strings.ToLower(w)] {
				keywordCount++
			}
		}

		// Score based on:
		// 1. Keyword density (keywords / words)
		// 2. Position bias (first and last sentences are more important)
		keywordDensity := 0.0
		if len(words) > 0 {
			keywordDensity = float64(keywordCount) / float64(len(words))
		}

		positionBias := 0.0
		if i == 0 {
			positionBias = 0.3 // First sentence bonus
		} else if i == len(sentences)-1 {
			positionBias = 0.2 // Last sentence bonus
		} else if i < len(sentences)/4 {
			positionBias = 0.1 // Early sentences bonus
		}

		// Length penalty for very short or very long sentences
		lengthPenalty := 0.0
		if len(words) < 5 {
			lengthPenalty = -0.2
		} else if len(words) > 50 {
			lengthPenalty = -0.1
		}

		scored[i] = scoredSentence{
			text:     sent,
			score:    keywordDensity + positionBias + lengthPenalty,
			position: i,
		}
	}

	return scored
}

// selectSentences selects sentences to meet target length while preserving order
func (fc *FallbackCompressor) selectSentences(scored []scoredSentence, targetLen int) []string {
	// Sort by score descending
	sorted := make([]scoredSentence, len(scored))
	copy(sorted, scored)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].score > sorted[j].score
	})

	// Select top sentences until we reach target length
	var selected []int
	currentLen := 0
	for _, s := range sorted {
		if currentLen+len(s.text) > targetLen && len(selected) > 0 {
			break
		}
		selected = append(selected, s.position)
		currentLen += len(s.text) + 1 // +1 for space
	}

	// Sort by original position to maintain narrative flow
	sort.Ints(selected)

	// Build result
	result := make([]string, len(selected))
	for i, pos := range selected {
		result[i] = scored[pos].text
	}
	return result
}

// tokenize splits text into words
func tokenize(text string) []string {
	var words []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(unicode.ToLower(r))
		} else if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

// truncateText truncates text to target length, trying to end at word boundary
func truncateText(text string, targetLen int) string {
	if len(text) <= targetLen {
		return text
	}

	// Find last space before target
	lastSpace := strings.LastIndex(text[:targetLen], " ")
	if lastSpace > targetLen/2 {
		return text[:lastSpace] + "..."
	}
	return text[:targetLen-3] + "..."
}

// defaultStopwords returns common English stopwords
func defaultStopwords() map[string]bool {
	words := []string{
		"a", "an", "the", "and", "or", "but", "in", "on", "at", "to", "for",
		"of", "with", "by", "from", "as", "is", "was", "are", "were", "been",
		"be", "have", "has", "had", "do", "does", "did", "will", "would",
		"could", "should", "may", "might", "must", "shall", "can", "need",
		"dare", "ought", "used", "it", "its", "they", "them", "their", "this",
		"that", "these", "those", "i", "you", "he", "she", "we", "who", "which",
		"what", "where", "when", "why", "how", "all", "each", "every", "both",
		"few", "more", "most", "other", "some", "such", "no", "nor", "not",
		"only", "own", "same", "so", "than", "too", "very", "just", "also",
		"now", "here", "there", "then", "once", "if", "about", "after",
		"before", "above", "below", "between", "under", "again", "further",
		"while", "during", "through", "into", "over", "any", "up", "down",
	}
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// CompressWithFallback attempts LLM compression, falls back to extractive
func CompressWithFallback(content string, targetRatio float64, summarizer func(string, int) (string, error)) CompressionResult {
	targetLen := int(float64(len(content)) * targetRatio)
	targetTokens := EstimateTokens(content) * int(targetRatio*100) / 100

	// Try LLM first
	if summarizer != nil {
		summary, err := summarizer(content, targetTokens)
		if err == nil && len(summary) > 0 {
			return CompressionResult{
				Summary:       summary,
				Method:        CompressionMethodLLM,
				OriginalLen:   len(content),
				CompressedLen: len(summary),
				Ratio:         float64(len(summary)) / float64(len(content)),
			}
		}
	}

	// Fall back to extractive compression
	fc := NewFallbackCompressor()
	result := fc.Compress(content, targetRatio)

	// If extractive didn't work well, try pure keyword extraction
	if result.Ratio > targetRatio*1.5 && len(result.Keywords) > 0 {
		keywords := fc.ExtractKeywords(content, 20)
		keywordSummary := "Key concepts: " + strings.Join(keywords, ", ")
		if len(keywordSummary) < targetLen {
			result.Summary = keywordSummary
			result.Method = CompressionMethodTFIDF
			result.CompressedLen = len(keywordSummary)
			result.Ratio = float64(len(keywordSummary)) / float64(len(content))
		}
	}

	return result
}
