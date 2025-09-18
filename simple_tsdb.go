package main

import (
	"context"
	"fmt"
	"io"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/histogram"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/prometheus/prometheus/util/annotations"
)

// SimpleStorage holds metrics in a simple format for querying
type SimpleStorage struct {
	metrics map[string][]MetricSample
}

// MetricSample represents a single metric sample
type MetricSample struct {
	Labels    map[string]string
	Value     float64
	Timestamp int64
}

// NewSimpleStorage creates a new simple storage
func NewSimpleStorage() *SimpleStorage {
	return &SimpleStorage{
		metrics: make(map[string][]MetricSample),
	}
}

// LoadFromReader loads Prometheus exposition format data using the official Prometheus parser
func (s *SimpleStorage) LoadFromReader(reader io.Reader) error {
	// Use the standard Prometheus exposition format parser
	parser := expfmt.NewTextParser(model.UTF8Validation)
	metricFamilies, err := parser.TextToMetricFamilies(reader)
	if err != nil {
		return fmt.Errorf("failed to parse metrics with Prometheus parser: %w", err)
	}

	// Process the parsed metric families
	return s.processMetricFamilies(metricFamilies)
}

// LoadFromReaderWithFilter loads metrics and applies a metric-name filter function.
// Only metric families for which filter(name) returns true are loaded.
func (s *SimpleStorage) LoadFromReaderWithFilter(reader io.Reader, filter func(name string) bool) error {
	parser := expfmt.NewTextParser(model.UTF8Validation)
	metricFamilies, err := parser.TextToMetricFamilies(reader)
	if err != nil {
		return fmt.Errorf("failed to parse metrics with Prometheus parser: %w", err)
	}
	if filter == nil {
		return s.processMetricFamilies(metricFamilies)
	}
	filtered := make(map[string]*dto.MetricFamily, len(metricFamilies))
	for name, mf := range metricFamilies {
		if filter(name) {
			filtered[name] = mf
		}
	}
	return s.processMetricFamilies(filtered)
}

// processMetricFamilies processes the parsed metric families (extracted from original LoadFromReader)
func (s *SimpleStorage) processMetricFamilies(metricFamilies map[string]*dto.MetricFamily) error {
	// Use a consistent base timestamp for all samples loaded in this call
	baseTimestamp := time.Now().UnixMilli()

	// Convert each metric family to individual samples
	for _, mf := range metricFamilies {
		metricName := mf.GetName()

		// Process each metric within the family
		for _, metric := range mf.GetMetric() {
			// Create labels map starting with the metric name
			lbls := make(map[string]string)
			lbls["__name__"] = metricName

			// Add all labels from the metric to our labels map
			for _, labelPair := range metric.GetLabel() {
				lbls[labelPair.GetName()] = labelPair.GetValue()
			}

			// Get value based on metric type
			var value float64
			// Always use the consistent base timestamp to keep samples within lookback
			timestamp := baseTimestamp

			switch mf.GetType() {
			case dto.MetricType_COUNTER:
				if metric.Counter != nil {
					value = metric.Counter.GetValue()
				}
			case dto.MetricType_GAUGE:
				if metric.Gauge != nil {
					value = metric.Gauge.GetValue()
				}
			case dto.MetricType_UNTYPED:
				// Handle untyped metrics - treat as gauge-like
				if metric.Untyped != nil {
					value = metric.Untyped.GetValue()
				}
			case dto.MetricType_HISTOGRAM:
				if metric.Histogram != nil {
					// Store histogram buckets as separate metrics
					for _, bucket := range metric.Histogram.GetBucket() {
						bucketLabels := make(map[string]string)
						for k, v := range lbls {
							bucketLabels[k] = v
						}
						bucketLabels["le"] = fmt.Sprintf("%g", bucket.GetUpperBound())
						bucketLabels["__name__"] = metricName + "_bucket"

						bucketSample := MetricSample{
							Labels:    bucketLabels,
							Value:     float64(bucket.GetCumulativeCount()),
							Timestamp: timestamp,
						}
						s.metrics[metricName+"_bucket"] = append(s.metrics[metricName+"_bucket"], bucketSample)
					}

					// Store histogram sum
					sumLabels := make(map[string]string)
					for k, v := range lbls {
						sumLabels[k] = v
					}
					sumLabels["__name__"] = metricName + "_sum"
					sumSample := MetricSample{
						Labels:    sumLabels,
						Value:     metric.Histogram.GetSampleSum(),
						Timestamp: timestamp,
					}
					s.metrics[metricName+"_sum"] = append(s.metrics[metricName+"_sum"], sumSample)

					// Store histogram count
					countLabels := make(map[string]string)
					for k, v := range lbls {
						countLabels[k] = v
					}
					countLabels["__name__"] = metricName + "_count"
					countSample := MetricSample{
						Labels:    countLabels,
						Value:     float64(metric.Histogram.GetSampleCount()),
						Timestamp: timestamp,
					}
					s.metrics[metricName+"_count"] = append(s.metrics[metricName+"_count"], countSample)
					continue
				}
			case dto.MetricType_SUMMARY:
				if metric.Summary != nil {
					// Store summary quantiles
					for _, quantile := range metric.Summary.GetQuantile() {
						quantileLabels := make(map[string]string)
						for k, v := range lbls {
							quantileLabels[k] = v
						}
						quantileLabels["quantile"] = fmt.Sprintf("%g", quantile.GetQuantile())

						quantileSample := MetricSample{
							Labels:    quantileLabels,
							Value:     quantile.GetValue(),
							Timestamp: timestamp,
						}
						s.metrics[metricName] = append(s.metrics[metricName], quantileSample)
					}

					// Store summary sum
					sumLabels := make(map[string]string)
					for k, v := range lbls {
						sumLabels[k] = v
					}
					sumLabels["__name__"] = metricName + "_sum"
					sumSample := MetricSample{
						Labels:    sumLabels,
						Value:     metric.Summary.GetSampleSum(),
						Timestamp: timestamp,
					}
					s.metrics[metricName+"_sum"] = append(s.metrics[metricName+"_sum"], sumSample)

					// Store summary count
					countLabels := make(map[string]string)
					for k, v := range lbls {
						countLabels[k] = v
					}
					countLabels["__name__"] = metricName + "_count"
					countSample := MetricSample{
						Labels:    countLabels,
						Value:     float64(metric.Summary.GetSampleCount()),
						Timestamp: timestamp,
					}
					s.metrics[metricName+"_count"] = append(s.metrics[metricName+"_count"], countSample)
					continue
				}
			default:
				continue
			}

			// Create and store the primary sample
			sample := MetricSample{
				Labels:    lbls,
				Value:     value,
				Timestamp: timestamp,
			}

			s.metrics[metricName] = append(s.metrics[metricName], sample)
		}
	}

	return nil
}

// Queryable implementation for SimpleStorage
func (s *SimpleStorage) Querier(mint, maxt int64) (storage.Querier, error) {
	return &SimpleQuerier{storage: s, mint: mint, maxt: maxt}, nil
}

// SimpleQuerier implements storage.Querier
type SimpleQuerier struct {
	storage    *SimpleStorage
	mint, maxt int64
}

func (q *SimpleQuerier) Select(ctx context.Context, sortSeries bool, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	var series []storage.Series

	// Find matching metrics
	for _, samples := range q.storage.metrics {
		// Check if any samples match the matchers and time range
		var matchingSamples []MetricSample
		for _, sample := range samples {
			// Check time range
			if sample.Timestamp < q.mint || sample.Timestamp > q.maxt {
				continue
			}

			// Check label matchers
			if q.matchesLabelMatchers(sample.Labels, matchers) {
				matchingSamples = append(matchingSamples, sample)
			}
		}

		// Group samples by unique label sets
		labelGroups := make(map[string][]MetricSample)
		for _, sample := range matchingSamples {
			key := q.getLabelKey(sample.Labels)
			labelGroups[key] = append(labelGroups[key], sample)
		}

		// Create a series for each unique label set
		for _, groupSamples := range labelGroups {
			if len(groupSamples) > 0 {
				lbls := labels.FromMap(groupSamples[0].Labels)
				series = append(series, &SimpleSeries{
					labels:  lbls,
					samples: groupSamples,
				})
			}
		}
	}

	return &SimpleSeriesSet{series: series, index: -1}
}

func (q *SimpleQuerier) LabelValues(ctx context.Context, name string, hints *storage.LabelHints, matchers ...*labels.Matcher) ([]string, annotations.Annotations, error) {
	values := make(map[string]struct{})
	for _, samples := range q.storage.metrics {
		for _, sample := range samples {
			if q.matchesLabelMatchers(sample.Labels, matchers) {
				if value, ok := sample.Labels[name]; ok {
					values[value] = struct{}{}
				}
			}
		}
	}

	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return result, nil, nil
}

func (q *SimpleQuerier) LabelNames(ctx context.Context, hints *storage.LabelHints, matchers ...*labels.Matcher) ([]string, annotations.Annotations, error) {
	names := make(map[string]struct{})
	for _, samples := range q.storage.metrics {
		for _, sample := range samples {
			if q.matchesLabelMatchers(sample.Labels, matchers) {
				for name := range sample.Labels {
					names[name] = struct{}{}
				}
			}
		}
	}

	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	return result, nil, nil
}

func (q *SimpleQuerier) Close() error {
	return nil
}

// matchesLabelMatchers checks if labels match the given matchers
func (q *SimpleQuerier) matchesLabelMatchers(sampleLabels map[string]string, matchers []*labels.Matcher) bool {
	for _, matcher := range matchers {
		value, exists := sampleLabels[matcher.Name]
		if !exists {
			value = ""
		}
		if !matcher.Matches(value) {
			return false
		}
	}
	return true
}

// getLabelKey creates a stable unique key for a label set by sorting labels
func (q *SimpleQuerier) getLabelKey(lbls map[string]string) string {
	l := labels.FromMap(lbls)
	return l.String()
}

// SimpleSeries implements storage.Series
type SimpleSeries struct {
	labels  labels.Labels
	samples []MetricSample
}

func (s *SimpleSeries) Labels() labels.Labels {
	return s.labels
}

func (s *SimpleSeries) Iterator(iterator chunkenc.Iterator) chunkenc.Iterator {
	return &SimpleIterator{samples: s.samples, index: -1}
}

// SimpleIterator implements chunkenc.Iterator
type SimpleIterator struct {
	samples []MetricSample
	index   int
}

func (it *SimpleIterator) Next() chunkenc.ValueType {
	it.index++
	if it.index >= len(it.samples) {
		return chunkenc.ValNone
	}
	return chunkenc.ValFloat
}

func (it *SimpleIterator) Seek(t int64) chunkenc.ValueType {
	for i, sample := range it.samples {
		if sample.Timestamp >= t {
			it.index = i
			return chunkenc.ValFloat
		}
	}
	it.index = len(it.samples)
	return chunkenc.ValNone
}

func (it *SimpleIterator) At() (int64, float64) {
	if it.index < 0 || it.index >= len(it.samples) {
		return 0, 0
	}
	sample := it.samples[it.index]
	return sample.Timestamp, sample.Value
}

func (it *SimpleIterator) AtHistogram(*histogram.Histogram) (int64, *histogram.Histogram) {
	return 0, nil
}

func (it *SimpleIterator) AtFloatHistogram(*histogram.FloatHistogram) (int64, *histogram.FloatHistogram) {
	return 0, nil
}

func (it *SimpleIterator) AtT() int64 {
	if it.index < 0 || it.index >= len(it.samples) {
		return 0
	}
	return it.samples[it.index].Timestamp
}

func (it *SimpleIterator) Err() error {
	return nil
}

// SimpleSeriesSet implements storage.SeriesSet
type SimpleSeriesSet struct {
	series []storage.Series
	index  int
	err    error
}

func (s *SimpleSeriesSet) Next() bool {
	s.index++
	return s.index < len(s.series)
}

func (s *SimpleSeriesSet) At() storage.Series {
	if s.index < 0 || s.index >= len(s.series) {
		return nil
	}
	return s.series[s.index]
}

func (s *SimpleSeriesSet) Err() error {
	return s.err
}

func (s *SimpleSeriesSet) Warnings() annotations.Annotations {
	return nil
}
