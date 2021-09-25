// k6 run --vus 10 --duration 5s http_post.js

import http from 'k6/http';
import { check } from 'k6';

export default function () {
    var url = 'http://localhost:8090/check';
    var payload = JSON.stringify({
        "urls" : [
            "https://www.google.com",
            "https://yandex.ru",
            "https://mail.ru?1",
            "https://mail.ru?2",
            "https://mail.ru?3",
            "https://mail.ru?4",
            "https://mail.ru?5"
        ]
    });

    var params = {
        headers: {
            'Content-Type': 'application/json',
        },
    };

    let res = http.post(url, payload, params);

    check(res, {
        'is status 200': (r) => r.status === 200,
    });
}
