INSERT INTO partners (id, name, lat, lng, available) VALUES
    ('a0000000-0000-0000-0000-000000000001', 'Aarav Kumar (Indiranagar)', 12.9784, 77.6408, true),
    ('a0000000-0000-0000-0000-000000000002', 'Bhavna Sharma (Koramangala)', 12.9352, 77.6245, true),
    ('a0000000-0000-0000-0000-000000000003', 'Chirag Patel (HSR Layout)', 12.9121, 77.6446, true),
    ('a0000000-0000-0000-0000-000000000004', 'Divya Rao (Whitefield)', 12.9698, 77.7499, true),
    ('a0000000-0000-0000-0000-000000000005', 'Eshan Reddy (Bellandur)', 12.9304, 77.6784, true),
    ('a0000000-0000-0000-0000-000000000006', 'Farhan Ali (Jayanagar)', 12.9308, 77.5838, true),
    ('a0000000-0000-0000-0000-000000000007', 'Gauri Joshi (JP Nagar)', 12.9063, 77.5857, true),
    ('a0000000-0000-0000-0000-000000000008', 'Harish Nair (MG Road)', 12.9756, 77.6066, true),
    ('a0000000-0000-0000-0000-000000000009', 'Ishita Verma (Malleshwaram)', 13.0031, 77.5643, true),
    ('a0000000-0000-0000-0000-000000000010', 'Jatin Das (Marathahalli)', 12.9591, 77.6974, true)
ON CONFLICT (id) DO NOTHING;
