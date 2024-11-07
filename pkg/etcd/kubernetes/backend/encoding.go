package backend

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"io"
)

func decode(compressedPayload []byte) (map[int64]*Event, error) {
	payload, err := decompress(compressedPayload)
	if err != nil {
		return nil, err
	}

	retArr := make([]*Event, 0, len(payload)/1024)
	err = gob.NewDecoder(bytes.NewReader(payload)).Decode(&retArr)
	if err != nil {
		return nil, err
	}

	retMap := map[int64]*Event{}
	for _, event := range retArr {
		retMap[event.Server.KV.ModRevision] = event
	}

	return retMap, nil
}

func encode(m map[int64]*Event) ([]byte, error) {
	eventArr := make([]*Event, 0, len(m))
	for _, serverEvent := range m {
		eventArr = append(eventArr, serverEvent)
	}

	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(eventArr)
	if err != nil {
		return nil, err
	}

	return compress(buf.Bytes())
}

func decompress(payload []byte) ([]byte, error) {
	rdata := bytes.NewReader(payload)
	r, err := gzip.NewReader(rdata)
	if err != nil {
		return nil, err
	}

	decompressed, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return decompressed, nil
}

func compress(s []byte) ([]byte, error) {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)

	_, err := gz.Write(s)
	if err != nil {
		return nil, err
	}

	err = gz.Flush()
	if err != nil {
		return nil, err
	}

	err = gz.Close()
	if err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}
